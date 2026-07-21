package permission_test

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/core/permission"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/mysql"
	"gorm.io/gorm"
)

func testService(t *testing.T) *permission.Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.Role{}); err != nil {
		t.Fatal(err)
	}
	svc, err := permission.NewService(db, mysql.NewRoleRepository(db))
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestRBACLifecycle(t *testing.T) {
	ctx := context.Background()
	svc := testService(t)
	if err := svc.Bootstrap(ctx, 1); err != nil {
		t.Fatal(err)
	}
	allowed, err := svc.Enforce(ctx, 1, "/api/v1/configs", "DELETE")
	if err != nil || !allowed {
		t.Fatalf("super admin allowed=%v err=%v", allowed, err)
	}
	role, err := svc.CreateRole(ctx, "config_reader", "reader", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CreateRole(ctx, "Bad-Role", "", false); err == nil {
		t.Fatal("expected invalid role")
	}
	if err = svc.SetPermissions(ctx, role.ID, []permission.Permission{{PathPattern: "/api/v1/configs", Methods: []string{"GET"}}}); err != nil {
		t.Fatal(err)
	}
	if err = svc.SetUserRoles(ctx, 2, []string{"config_reader"}); err != nil {
		t.Fatal(err)
	}
	roles, err := svc.GetUserRoles(ctx, 2)
	if err != nil || len(roles) != 1 || roles[0] != "config_reader" {
		t.Fatalf("roles=%v err=%v", roles, err)
	}
	allowed, _ = svc.Enforce(ctx, 2, "/api/v1/configs", "GET")
	if !allowed {
		t.Fatal("reader should read")
	}
	allowed, _ = svc.Enforce(ctx, 2, "/api/v1/configs", "POST")
	if allowed {
		t.Fatal("reader must not write")
	}
	permissions, err := svc.GetPermissions(ctx, role.ID)
	if err != nil || len(permissions) != 1 {
		t.Fatalf("permissions=%v err=%v", permissions, err)
	}
	if err = svc.DeleteRole(ctx, role.ID); err == nil {
		t.Fatal("role in use must not delete")
	}
	if err = svc.SetUserRoles(ctx, 2, nil); err != nil {
		t.Fatal(err)
	}
	if err = svc.DeleteRole(ctx, role.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSuperAdminProtection(t *testing.T) {
	ctx := context.Background()
	svc := testService(t)
	if err := svc.Bootstrap(ctx, 1); err != nil {
		t.Fatal(err)
	}
	ok, err := svc.CanDisable(ctx, 1)
	if err != nil || ok {
		t.Fatalf("last super admin ok=%v err=%v", ok, err)
	}
	if err = svc.SetUserRoles(ctx, 1, nil); err == nil {
		t.Fatal("must protect last super admin")
	}
	if err = svc.Bootstrap(ctx, 2); err != nil {
		t.Fatal(err)
	}
	ok, err = svc.CanDisable(ctx, 1)
	if err != nil || !ok {
		t.Fatalf("two super admins ok=%v err=%v", ok, err)
	}
	if err = svc.SetUserRoles(ctx, 1, nil); err != nil {
		t.Fatal(err)
	}
	roles, _, err := svc.ListRoles(ctx, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	var superID uint64
	for _, role := range roles {
		if role.Name == model.SuperAdminRole {
			superID = role.ID
			break
		}
	}
	if superID == 0 {
		t.Fatal("super admin role not found")
	}
	if err = svc.DeleteRole(ctx, superID); err == nil {
		t.Fatal("builtin role must not delete")
	}
	if err = svc.SetPermissions(ctx, superID, nil); err == nil {
		t.Fatal("super admin permissions must not change")
	}
}

func TestPermissionValidation(t *testing.T) {
	ctx := context.Background()
	svc := testService(t)
	role, err := svc.CreateRole(ctx, "reader", "", false)
	if err != nil {
		t.Fatal(err)
	}
	cases := [][]permission.Permission{{{PathPattern: "/wrong", Methods: []string{"GET"}}}, {{PathPattern: "/api/v1/x", Methods: nil}}, {{PathPattern: "/api/v1/x", Methods: []string{"TRACE"}}}}
	for _, permissions := range cases {
		if err = svc.SetPermissions(ctx, role.ID, permissions); err == nil {
			t.Fatalf("expected validation failure for %#v", permissions)
		}
	}
	if err = svc.SetUserRoles(ctx, 3, []string{"missing"}); err == nil {
		t.Fatal("unknown role must fail")
	}
}

func TestRoleMetadata(t *testing.T) {
	ctx := context.Background()
	svc := testService(t)
	role, err := svc.CreateRole(ctx, "moderator", "old", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CreateRole(ctx, "moderator", "duplicate", false); err == nil {
		t.Fatal("duplicate role must fail")
	}
	updated, err := svc.UpdateRole(ctx, role.ID, "new")
	if err != nil || updated.Description != "new" {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
	rows, total, err := svc.ListRoles(ctx, 1, 20)
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("rows=%v total=%d err=%v", rows, total, err)
	}
	if _, err = svc.GetRole(ctx, 999); err == nil {
		t.Fatal("missing role must fail")
	}
	if err = svc.SetPermissions(ctx, role.ID, []permission.Permission{{PathPattern: "/api/v1/items/:id", Methods: []string{"get", "delete"}}}); err != nil {
		t.Fatal(err)
	}
	allowed, _ := svc.Enforce(ctx, 42, "/api/v1/items/1", "GET")
	if allowed {
		t.Fatal("unassigned user must be denied")
	}
}
