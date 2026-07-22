// Package httpapi exposes the platform REST API.
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
)

const requestIDKey = "request_id"
const userIDKey = "user_id"
const sessionIDKey = "session_id"

func success(c *gin.Context, status int, data any) {
	c.JSON(status, gin.H{"data": data, "request_id": c.GetString(requestIDKey)})
}
func failure(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	message := "服务内部错误"
	if app, ok := apperror.As(err); ok {
		status = app.Status
		code = app.Code
		message = app.Message
	}
	if err != nil {
		_ = c.Error(err)
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}, "request_id": c.GetString(requestIDKey)})
}
func pageData(items any, page, size int, total int64) gin.H {
	return gin.H{"items": items, "page": page, "page_size": size, "total": total}
}
