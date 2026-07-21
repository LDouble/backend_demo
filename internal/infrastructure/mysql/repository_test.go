package mysql_test

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	platformmysql "github.com/weouc-plus/campus-platform/internal/infrastructure/mysql"
	"gorm.io/gorm"
)

func TestGeneratedQueryRepositories(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T, *gorm.DB)
	}{
		{name: "用户仓储", run: testUserRepository},
		{name: "角色仓储", run: testRoleRepository},
		{name: "配置仓储与乐观锁", run: testConfigRepository},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			tt.run(t, db)
		})
	}
}

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("打开测试数据库: %v", err)
	}
	if err = db.AutoMigrate(&model.User{}, &model.Role{}, &model.Config{}); err != nil {
		t.Fatalf("迁移测试数据库: %v", err)
	}
	return db
}

func testUserRepository(t *testing.T, db *gorm.DB) {
	t.Helper()
	ctx := context.Background()
	repository := platformmysql.NewUserRepository(db)
	created := &model.User{Username: "alice", PasswordHash: "hash", Status: model.UserActive}
	if err := repository.Create(ctx, created); err != nil {
		t.Fatalf("创建用户: %v", err)
	}
	got, err := repository.GetByUsername(ctx, created.Username)
	if err != nil || got.ID != created.ID {
		t.Fatalf("按用户名查询: got=%v err=%v", got, err)
	}
	got.Status = model.UserDisabled
	if err = repository.Update(ctx, got); err != nil {
		t.Fatalf("更新用户: %v", err)
	}
	rows, total, err := repository.List(ctx, 1, 10)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].Status != model.UserDisabled {
		t.Fatalf("用户列表: rows=%v total=%d err=%v", rows, total, err)
	}
}

func testRoleRepository(t *testing.T, db *gorm.DB) {
	t.Helper()
	ctx := context.Background()
	repository := platformmysql.NewRoleRepository(db)
	created := &model.Role{Name: "reader", Description: "只读角色"}
	if err := repository.Create(ctx, created); err != nil {
		t.Fatalf("创建角色: %v", err)
	}
	got, err := repository.GetByName(ctx, created.Name)
	if err != nil || got.ID != created.ID {
		t.Fatalf("按名称查询角色: got=%v err=%v", got, err)
	}
	got.Description = "读取配置"
	if err = repository.Update(ctx, got); err != nil {
		t.Fatalf("更新角色: %v", err)
	}
	rows, total, err := repository.List(ctx, 1, 10)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].Description != got.Description {
		t.Fatalf("角色列表: rows=%v total=%d err=%v", rows, total, err)
	}
	if err = repository.Delete(ctx, created.ID); err != nil {
		t.Fatalf("删除角色: %v", err)
	}
	if _, err = repository.Get(ctx, created.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("已删除角色仍可查询: %v", err)
	}
}

func testConfigRepository(t *testing.T, db *gorm.DB) {
	t.Helper()
	ctx := context.Background()
	repository := platformmysql.NewConfigRepository(db)
	created := &model.Config{Group: "security", Key: "token", Value: "ciphertext", Encrypted: true, Version: 1, UpdatedBy: 1}
	if err := repository.Create(ctx, created); err != nil {
		t.Fatalf("创建配置: %v", err)
	}
	rows, total, err := repository.List(ctx, "security", 1, 10)
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("按分组查询配置: rows=%v total=%d err=%v", rows, total, err)
	}
	created.Value = "new-ciphertext"
	created.Version = 2
	updated, err := repository.UpdateVersion(ctx, created, 1)
	if err != nil || !updated {
		t.Fatalf("乐观锁更新: updated=%v err=%v", updated, err)
	}
	updated, err = repository.UpdateVersion(ctx, created, 1)
	if err != nil || updated {
		t.Fatalf("旧版本应冲突: updated=%v err=%v", updated, err)
	}
	got, err := repository.Get(ctx, created.ID)
	if err != nil || got.Version != 2 || got.Value != created.Value {
		t.Fatalf("查询更新后的配置: got=%v err=%v", got, err)
	}
	if err = repository.Delete(ctx, created.ID); err != nil {
		t.Fatalf("删除配置: %v", err)
	}
}
