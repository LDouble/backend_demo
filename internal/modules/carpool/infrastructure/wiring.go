package infrastructure

import (
	"gorm.io/gorm"

	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	carpoolapp "github.com/weouc-plus/campus-platform/internal/modules/carpool/application"
)

// NewManager constructs the carpool Manager from shared infrastructure.
func NewManager(db *gorm.DB, cipher *configcenter.Cipher) *carpoolapp.Manager {
	return carpoolapp.NewManager(NewStore(db, cipher))
}
