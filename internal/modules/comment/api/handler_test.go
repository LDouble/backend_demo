package api

import (
	"testing"

	"github.com/weouc-plus/campus-platform/internal/modules/comment/application"
)

func TestNewHandler(t *testing.T) {
	service := application.NewService(nil)
	handler := NewHandler(service)
	if handler == nil || handler.service != service {
		t.Fatal("comment handler did not retain service")
	}
}
