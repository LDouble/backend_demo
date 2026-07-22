package httpapi

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"go.uber.org/zap"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

const maxRequestIDLength = 36

func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.GetHeader("X-Request-ID"))
		if !validRequestID(id) {
			id = uuid.NewString()
		}
		c.Set(requestIDKey, id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

func validRequestID(value string) bool {
	if value == "" || len(value) > maxRequestIDLength {
		return false
	}
	if _, err := uuid.Parse(value); err == nil {
		return true
	}
	if len(value) != 26 {
		return false
	}
	const ulidAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	for _, char := range strings.ToUpper(value) {
		if !strings.ContainsRune(ulidAlphabet, char) {
			return false
		}
	}
	return true
}

func requestLimits(maxBodyBytes int64, maxHeaderBytes int) gin.HandlerFunc {
	return func(c *gin.Context) {
		headerBytes := 0
		for name, values := range c.Request.Header {
			headerBytes += len(name)
			for _, value := range values {
				headerBytes += len(value)
			}
		}
		if headerBytes > maxHeaderBytes {
			failure(c, apperror.New(http.StatusRequestHeaderFieldsTooLarge, "headers_too_large", "请求头过大"))
			return
		}
		if c.Request.ContentLength > maxBodyBytes {
			failure(c, apperror.New(http.StatusRequestEntityTooLarge, "body_too_large", "请求体过大"))
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		c.Next()
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		c.Header("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Next()
	}
}
func recovery(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Error("panic recovered", zap.Any("panic", recovered), zap.ByteString("stack", debug.Stack()), zap.String("request_id", c.GetString(requestIDKey)))
				failure(c, apperror.New(500, "internal_error", "服务内部错误"))
			}
		}()
		c.Next()
	}
}
func accessLog(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		fields := []zap.Field{zap.String("request_id", c.GetString(requestIDKey)), zap.Uint64("user_id", c.GetUint64(userIDKey)), zap.String("path", c.Request.URL.Path), zap.String("method", c.Request.Method), zap.Int("status", c.Writer.Status()), zap.Duration("latency", time.Since(started))}
		if len(c.Errors) > 0 {
			fields = append(fields, zap.String("error", c.Errors.String()))
		}
		log.Info("http request", fields...)
	}
}

func (h *Handler) security() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if path == "/health/live" || path == "/health/ready" || path == "/api/v1/auth/login" || path == "/api/v1/auth/refresh" || (c.Request.Method == http.MethodGet && strings.HasPrefix(path, "/api/v1/runtime-configs/")) {
			c.Next()
			return
		}
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			failure(c, apperror.New(http.StatusUnauthorized, "missing_token", "缺少访问令牌"))
			return
		}
		uid, sid, err := h.auth.Authenticate(c.Request.Context(), strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
		if err != nil {
			failure(c, err)
			return
		}
		c.Set(userIDKey, uid)
		c.Set(sessionIDKey, sid)
		if path == "/api/v1/auth/logout" || path == "/api/v1/auth/me" {
			c.Next()
			return
		}
		ok, err := h.permissions.Enforce(c.Request.Context(), c.GetUint64(userIDKey), c.Request.URL.Path, c.Request.Method)
		if err != nil {
			failure(c, err)
			return
		}
		if !ok {
			failure(c, apperror.New(http.StatusForbidden, "forbidden", "无权执行此操作"))
			return
		}
		c.Next()
	}
}
