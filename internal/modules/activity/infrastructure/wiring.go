// Package infrastructure wires the activity store and manager behind a single
// constructor so the runtime no longer hand-wires Manager+Store separately.
// This is the surface the generated module.go calls into.
package infrastructure

import (
	"gorm.io/gorm"

	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/modules/activity/application"
)

// NewManager constructs the activity Manager from shared infrastructure.
// The cipher may be nil for environments that do not require contact
// encryption; in that case a placeholder cipher from configcenter is wired
// so the Store's AAD checks return a stable "cipher not configured" envelope.
func NewManager(db *gorm.DB, cipher *configcenter.Cipher) *application.Manager {
	return application.NewManager(NewStore(db, cipher))
}
