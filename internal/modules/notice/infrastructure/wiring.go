package infrastructure

import (
	"gorm.io/gorm"

	noticeapp "github.com/weouc-plus/campus-platform/internal/modules/notice/application"
)

// NewManager constructs the notice Manager from shared infrastructure.
// Notice does not need contact encryption, so it ignores the cipher argument.
func NewManager(db *gorm.DB, _ interface{}) *noticeapp.Manager {
	return noticeapp.NewManager(NewNoticeStore(db))
}
