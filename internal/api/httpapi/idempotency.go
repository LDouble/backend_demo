package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type bufferedResponseWriter struct {
	gin.ResponseWriter
	header http.Header
	body   bytes.Buffer
	status int
	size   int
}

type bufferedHandlerError struct {
	status  int
	body    []byte
	headers http.Header
}

func (e *bufferedHandlerError) Error() string {
	return fmt.Sprintf("handler failed with HTTP status %d", e.status)
}

func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *bufferedResponseWriter) Header() http.Header { return w.header }

func (w *bufferedResponseWriter) WriteHeaderNow() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
}

func (w *bufferedResponseWriter) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	n, err := w.body.Write(data)
	w.size += n
	return n, err
}

func (w *bufferedResponseWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}

func (w *bufferedResponseWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *bufferedResponseWriter) Size() int     { return w.size }
func (w *bufferedResponseWriter) Written() bool { return w.status != 0 }

func (h *Handler) idempotent(c *gin.Context, operationID string, work func()) {
	if h.db == nil {
		failure(c, fmt.Errorf("idempotency database is unavailable"))
		return
	}
	requestHash, err := canonicalRequestHash(c)
	if err != nil {
		failure(c, err)
		return
	}
	originalWriter := c.Writer
	executionContext, afterCommit := idempotency.WithAfterCommit(c.Request.Context())
	result, err := idempotency.Execute(
		executionContext,
		h.db,
		idempotency.Request{
			ActorID: c.GetUint64(userIDKey), OperationID: operationID,
			Key: c.GetHeader("Idempotency-Key"), RequestHash: requestHash,
		},
		func(tx *gorm.DB) (idempotency.Result, error) {
			buffered := &bufferedResponseWriter{ResponseWriter: originalWriter, header: make(http.Header)}
			originalRequest := c.Request
			defer func() {
				c.Writer = originalWriter
				c.Request = originalRequest
			}()
			c.Writer = buffered
			request := c.Request.WithContext(idempotency.WithTransaction(executionContext, tx))
			c.Request = request
			work()
			body := append([]byte(nil), buffered.body.Bytes()...)
			headers := replayableHeaders(buffered.Header())
			if buffered.Status() >= http.StatusBadRequest {
				return idempotency.Result{}, &bufferedHandlerError{status: buffered.Status(), body: body, headers: headers}
			}
			return idempotency.Result{HTTPStatus: buffered.Status(), Body: body, Headers: headers}, nil
		},
	)
	c.Writer = originalWriter
	if err != nil {
		var handlerErr *bufferedHandlerError
		if errors.As(err, &handlerErr) {
			applyReplayHeaders(originalWriter.Header(), handlerErr.headers)
			originalWriter.WriteHeader(handlerErr.status)
			if _, writeErr := originalWriter.Write(handlerErr.body); writeErr != nil {
				h.log.Error("write rolled back handler response", zap.Error(writeErr), zap.String("operation_id", operationID))
			}
			return
		}
		failure(c, err)
		return
	}
	if callbackErr := afterCommit.Run(executionContext); callbackErr != nil {
		h.log.Error("run idempotency after-commit callbacks", zap.Error(callbackErr), zap.String("operation_id", operationID))
	}
	applyReplayHeaders(originalWriter.Header(), result.Headers)
	originalWriter.WriteHeader(result.HTTPStatus)
	if _, writeErr := originalWriter.Write(result.Body); writeErr != nil {
		h.log.Error("write idempotency response", zap.Error(writeErr), zap.String("operation_id", operationID))
	}
}

func canonicalRequestHash(c *gin.Context) (string, error) {
	body := any(nil)
	if c.Request.Body != nil {
		raw, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return "", fmt.Errorf("read idempotency request body: %w", err)
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		if len(bytes.TrimSpace(raw)) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if err = decoder.Decode(&body); err != nil {
				return "", fmt.Errorf("decode idempotency request body: %w", err)
			}
			var extra any
			if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
				return "", fmt.Errorf("decode idempotency request body: multiple JSON values")
			}
		}
	}
	return idempotency.RequestHash(struct {
		Path  string
		Query map[string][]string
		Body  any
	}{Path: c.Request.URL.Path, Query: c.Request.URL.Query(), Body: body})
}

var replayableResponseHeaders = []string{"Content-Type", "Location", "ETag", "Cache-Control"}

func replayableHeaders(header http.Header) http.Header {
	result := make(http.Header)
	for _, name := range replayableResponseHeaders {
		if values := header.Values(name); len(values) > 0 {
			result[name] = append([]string(nil), values...)
		}
	}
	return result
}

func applyReplayHeaders(destination, source http.Header) {
	for _, name := range replayableResponseHeaders {
		destination.Del(name)
		for _, value := range source.Values(name) {
			destination.Add(name, value)
		}
	}
}
