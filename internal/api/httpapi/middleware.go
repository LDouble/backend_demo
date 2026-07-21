package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"go.uber.org/zap"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.GetHeader("X-Request-ID"))
		if id == "" {
			b := make([]byte, 16)
			if _, err := rand.Read(b); err == nil {
				id = hex.EncodeToString(b)
			} else {
				id = "unavailable"
			}
		}
		c.Set(requestIDKey, id)
		c.Header("X-Request-ID", id)
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
		if path == "/health/live" || path == "/health/ready" || path == "/api/v1/auth/login" || path == "/api/v1/auth/refresh" {
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
