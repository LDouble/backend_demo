package httpapi

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFailureEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(requestID())
	r.GET("/", func(c *gin.Context) { failure(c, apperror.New(409, "conflict", "冲突")) })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	requestID := "7cf0bd65-79b3-4d60-89d0-8fe8b827fd41"
	req.Header.Set("X-Request-ID", requestID)
	r.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Fatalf("status %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["request_id"] != requestID {
		t.Fatalf("body %#v", body)
	}
}
