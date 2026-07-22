package infrastructure

import (
	"gorm.io/gorm"

	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	marketplaceapp "github.com/weouc-plus/campus-platform/internal/modules/marketplace/application"
)

// NewManager constructs the marketplace Manager from shared infrastructure.
func NewManager(db *gorm.DB, cipher *configcenter.Cipher) *marketplaceapp.Manager {
	return marketplaceapp.NewManager(NewStore(db, cipher))
}
