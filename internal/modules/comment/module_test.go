package comment

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestNew(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	module := New(db)
	if module == nil || module.Handler == nil {
		t.Fatal("comment module was not wired")
	}
}
