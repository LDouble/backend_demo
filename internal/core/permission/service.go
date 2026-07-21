// Package permission implements persisted Casbin RBAC.
package permission

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/casbin/casbin/v3"
	casbinmodel "github.com/casbin/casbin/v3/model"
	gormadapter "github.com/casbin/gorm-adapter/v3"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"gorm.io/gorm"
)

const modelText = `[request_definition]
r = sub, obj, act
[policy_definition]
p = sub, obj, act
[role_definition]
g = _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub) && keyMatch2(r.obj, p.obj) && regexMatch(r.act, p.act)`

var rolePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`)

// RoleRepository persists role metadata.
type RoleRepository interface {
	Create(context.Context, *model.Role) error
	Get(context.Context, uint64) (*model.Role, error)
	GetByName(context.Context, string) (*model.Role, error)
	List(context.Context, int, int) ([]model.Role, int64, error)
	Update(context.Context, *model.Role) error
	Delete(context.Context, uint64) error
}

// Permission describes an API permission.
type Permission struct {
	PathPattern string   `json:"path_pattern"`
	Methods     []string `json:"methods"`
}

// Service manages roles and authorization policies.
type Service struct {
	roles    RoleRepository
	enforcer *casbin.SyncedEnforcer
}

// NewService creates a DB-backed Casbin service.
func NewService(db *gorm.DB, roles RoleRepository) (*Service, error) {
	a, err := gormadapter.NewAdapterByDBUseTableName(db, "", "casbin_rule")
	if err != nil {
		return nil, fmt.Errorf("create casbin adapter: %w", err)
	}
	m, err := casbinmodel.NewModelFromString(modelText)
	if err != nil {
		return nil, fmt.Errorf("parse casbin model: %w", err)
	}
	e, err := casbin.NewSyncedEnforcer(m, a)
	if err != nil {
		return nil, fmt.Errorf("create casbin enforcer: %w", err)
	}
	return &Service{roles: roles, enforcer: e}, nil
}

func subject(id uint64) string { return "user:" + strconv.FormatUint(id, 10) }

// Enforce checks whether a user may call an API path and method.
func (s *Service) Enforce(ctx context.Context, userID uint64, path, method string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	ok, err := s.enforcer.Enforce(subject(userID), path, method)
	if err != nil {
		return false, fmt.Errorf("enforce permission: %w", err)
	}
	return ok, nil
}

// CreateRole creates role metadata.
func (s *Service) CreateRole(ctx context.Context, name, description string, builtin bool) (*model.Role, error) {
	name = strings.TrimSpace(name)
	if !rolePattern.MatchString(name) {
		return nil, apperror.New(400, "invalid_role_name", "角色名格式无效")
	}
	r := &model.Role{Name: name, Description: strings.TrimSpace(description), Builtin: builtin}
	if err := s.roles.Create(ctx, r); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, apperror.New(409, "role_exists", "角色已存在")
		}
		return nil, fmt.Errorf("create role: %w", err)
	}
	return r, nil
}

// GetRole returns role metadata by ID.
func (s *Service) GetRole(ctx context.Context, id uint64) (*model.Role, error) {
	r, err := s.roles.Get(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.New(404, "role_not_found", "角色不存在")
	}
	return r, err
}

// ListRoles returns a page of role metadata.
func (s *Service) ListRoles(ctx context.Context, page, size int) ([]model.Role, int64, error) {
	return s.roles.List(ctx, page, size)
}

// UpdateRole changes a role description.
func (s *Service) UpdateRole(ctx context.Context, id uint64, description string) (*model.Role, error) {
	r, err := s.GetRole(ctx, id)
	if err != nil {
		return nil, err
	}
	r.Description = strings.TrimSpace(description)
	if err := s.roles.Update(ctx, r); err != nil {
		return nil, fmt.Errorf("update role: %w", err)
	}
	return r, nil
}

// DeleteRole removes an unused non-built-in role.
func (s *Service) DeleteRole(ctx context.Context, id uint64) error {
	r, err := s.GetRole(ctx, id)
	if err != nil {
		return err
	}
	if r.Builtin {
		return apperror.New(http.StatusConflict, "builtin_role", "内置角色不能删除")
	}
	users, err := s.enforcer.GetUsersForRole(r.Name)
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return apperror.New(409, "role_in_use", "角色仍被用户使用")
	}
	if _, err := s.enforcer.DeleteRole(r.Name); err != nil {
		return fmt.Errorf("delete casbin role: %w", err)
	}
	return s.roles.Delete(ctx, id)
}

// GetUserRoles returns direct roles for a user.
func (s *Service) GetUserRoles(ctx context.Context, userID uint64) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	roles, err := s.enforcer.GetRolesForUser(subject(userID))
	sort.Strings(roles)
	return roles, err
}

// SetUserRoles replaces direct roles for a user.
func (s *Service) SetUserRoles(ctx context.Context, userID uint64, roles []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized := unique(append(roles, model.MemberRole))
	for _, name := range normalized {
		if _, err := s.roles.GetByName(ctx, name); errors.Is(err, gorm.ErrRecordNotFound) {
			return apperror.New(400, "unknown_role", "角色不存在: "+name)
		} else if err != nil {
			return err
		}
	}
	current, err := s.enforcer.GetRolesForUser(subject(userID))
	if err != nil {
		return err
	}
	if contains(current, model.SuperAdminRole) && !contains(normalized, model.SuperAdminRole) {
		ok, e := s.CanDisable(ctx, userID)
		if e != nil {
			return e
		}
		if !ok {
			return apperror.New(409, "last_super_admin", "不能移除最后一个超级管理员")
		}
	}
	if _, err = s.enforcer.DeleteRolesForUser(subject(userID)); err != nil {
		return fmt.Errorf("clear user roles: %w", err)
	}
	if len(normalized) > 0 {
		if _, err = s.enforcer.AddRolesForUser(subject(userID), normalized); err != nil {
			return fmt.Errorf("set user roles: %w", err)
		}
	}
	return nil
}

// GetPermissions returns API permissions assigned to a role.
func (s *Service) GetPermissions(ctx context.Context, roleID uint64) ([]Permission, error) {
	r, err := s.GetRole(ctx, roleID)
	if err != nil {
		return nil, err
	}
	rows, err := s.enforcer.GetPermissionsForUser(r.Name)
	if err != nil {
		return nil, err
	}
	out := make([]Permission, 0, len(rows))
	for _, row := range rows {
		if len(row) >= 3 {
			out = append(out, Permission{PathPattern: row[1], Methods: strings.Split(strings.Trim(row[2], "()"), "|")})
		}
	}
	return out, nil
}

// SetPermissions replaces API permissions assigned to a role.
func (s *Service) SetPermissions(ctx context.Context, roleID uint64, permissions []Permission) error {
	r, err := s.GetRole(ctx, roleID)
	if err != nil {
		return err
	}
	if r.Builtin {
		return apperror.New(409, "builtin_role", "内置角色权限不可修改")
	}
	rules := make([][]string, 0, len(permissions))
	for _, p := range permissions {
		if !strings.HasPrefix(p.PathPattern, "/api/v1/") {
			return apperror.New(400, "invalid_permission", "权限路径必须以 /api/v1/ 开头")
		}
		methods := uniqueUpper(p.Methods)
		if len(methods) == 0 {
			return apperror.New(400, "invalid_permission", "权限方法不能为空")
		}
		for _, m := range methods {
			if !contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, m) {
				return apperror.New(400, "invalid_permission", "不支持的 HTTP 方法")
			}
		}
		expr := methods[0]
		if len(methods) > 1 {
			expr = "(" + strings.Join(methods, "|") + ")"
		}
		rules = append(rules, []string{r.Name, p.PathPattern, expr})
	}
	if _, err = s.enforcer.DeletePermissionsForUser(r.Name); err != nil {
		return err
	}
	if len(rules) > 0 {
		if _, err = s.enforcer.AddPolicies(rules); err != nil {
			return err
		}
	}
	return nil
}

// CanDisable reports whether disabling a user preserves a super administrator.
func (s *Service) CanDisable(ctx context.Context, userID uint64) (bool, error) {
	roles, err := s.GetUserRoles(ctx, userID)
	if err != nil {
		return false, err
	}
	if !contains(roles, model.SuperAdminRole) {
		return true, nil
	}
	users, err := s.enforcer.GetUsersForRole(model.SuperAdminRole)
	return len(users) > 1, err
}

// Bootstrap idempotently creates and assigns the built-in super administrator role.
func (s *Service) Bootstrap(ctx context.Context, userID uint64) error {
	if err := s.EnsureMemberForUser(ctx, userID); err != nil {
		return err
	}
	r, err := s.roles.GetByName(ctx, model.SuperAdminRole)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		r, err = s.CreateRole(ctx, model.SuperAdminRole, "系统超级管理员", true)
	}
	if err != nil {
		return err
	}
	_ = r
	if _, err = s.enforcer.AddPermissionForUser(model.SuperAdminRole, "/api/v1/*", ".*"); err != nil {
		return err
	}
	_, err = s.enforcer.AddRoleForUser(subject(userID), model.SuperAdminRole)
	return err
}

// EnsureMemberForUser creates the built-in member role and grants its fixed
// personal-notification permissions before assigning it to a user.
func (s *Service) EnsureMemberForUser(ctx context.Context, userID uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	member, err := s.roles.GetByName(ctx, model.MemberRole)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		member, err = s.CreateRole(ctx, model.MemberRole, "平台成员", true)
	}
	if err != nil && !errors.Is(err, gorm.ErrDuplicatedKey) {
		return err
	}
	if member != nil && !member.Builtin {
		member.Builtin = true
		if err = s.roles.Update(ctx, member); err != nil {
			return fmt.Errorf("mark member role built-in: %w", err)
		}
	}
	permissions := [][]string{
		{model.MemberRole, "/api/v1/notices", "GET"},
		{model.MemberRole, "/api/v1/notices/unread-count", "GET"},
		{model.MemberRole, "/api/v1/notices/read-all", "PUT"},
		{model.MemberRole, "/api/v1/notices/:id", "GET"},
		{model.MemberRole, "/api/v1/notices/:id/read", "PUT"},
	}
	if _, err = s.enforcer.AddPolicies(permissions); err != nil {
		return err
	}
	_, err = s.enforcer.AddRoleForUser(subject(userID), model.MemberRole)
	return err
}
func unique(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" {
			if _, ok := seen[v]; !ok {
				seen[v] = struct{}{}
				out = append(out, v)
			}
		}
	}
	sort.Strings(out)
	return out
}
func uniqueUpper(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = strings.ToUpper(strings.TrimSpace(v))
	}
	return unique(out)
}
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
