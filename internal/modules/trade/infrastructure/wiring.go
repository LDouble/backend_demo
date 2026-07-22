package infrastructure

import (
	"gorm.io/gorm"

	tradeapp "github.com/weouc-plus/campus-platform/internal/modules/trade/application"
)

// NewManager constructs the trade Manager from shared infrastructure.
// Trade does not need contact encryption, so it ignores the cipher argument.
func NewManager(db *gorm.DB, _ interface{}) *tradeapp.Manager {
	return tradeapp.NewManager(NewStore(db))
}
