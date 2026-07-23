package infrastructure

import (
	"github.com/weouc-plus/campus-platform/internal/modules/campus_circle/application"
	"gorm.io/gorm"
)

// NewManager constructs the campus-circle manager from shared infrastructure.
func NewManager(db *gorm.DB) *application.Manager {
	return application.NewManager(NewStore(db))
}
