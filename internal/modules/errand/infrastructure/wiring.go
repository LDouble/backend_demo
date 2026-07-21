package infrastructure

import (
	"gorm.io/gorm"

	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	errandapp "github.com/weouc-plus/campus-platform/internal/modules/errand/application"
)

// NewManager constructs the errand Manager from shared infrastructure.
func NewManager(db *gorm.DB, cipher *configcenter.Cipher) *errandapp.Manager {
	return errandapp.NewManager(NewStore(db, cipher))
}
