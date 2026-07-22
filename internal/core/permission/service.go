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
	"sync"
	"sync/atomic"

	"github.com/casbin/casbin/v3"
	casbinmodel "github.com/casbin/casbin/v3/model"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	permissionmanifest "github.com/weouc-plus/campus-platform/permissions"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	UpdateDescription(context.Context, uint64, string) error
	Delete(context.Context, uint64) error
}

// Permission describes an API permission.
type Permission struct {
	PathPattern string   `json:"path_pattern"`
	Methods     []string `json:"methods"`
}

// Service manages roles and authorization policies.
type Service struct {
	db                *gorm.DB
	roles             RoleRepository
	enforcer          atomic.Pointer[casbin.SyncedEnforcer]
	reloadMu          sync.Mutex
	memberPolicies    []policyRule
	guestPolicies     []policyRule
	permissionCatalog map[string]map[string]struct{}
	catalogEntries    []permissionmanifest.CatalogEntry
	sync              policySync
	log               *zap.Logger
}

// NewService creates a DB-backed Casbin service.
func NewService(ctx context.Context, db *gorm.DB, roles RoleRepository) (*Service, error) {
	e, err := newPolicyEnforcer(ctx, db)
	if err != nil {
		return nil, err
	}
	manifestRules, err := permissionmanifest.MemberRules()
	if err != nil {
		return nil, err
	}
	memberPolicies := make([]policyRule, 0, len(manifestRules))
	for _, rule := range manifestRules {
		methods := uniqueUpper(rule.Methods)
		expression := strings.Join(methods, "|")
		if len(methods) > 1 {
			expression = "(" + expression + ")"
		}
		row := newPolicyRule("p", []string{model.MemberRole, rule.PathPattern, expression})
		row.Managed = true
		memberPolicies = append(memberPolicies, row)
	}
	guestRules, err := permissionmanifest.RulesForRole(model.GuestRole)
	if err != nil {
		return nil, err
	}
	guestPolicies := make([]policyRule, 0, len(guestRules))
	for _, rule := range guestRules {
		methods := uniqueUpper(rule.Methods)
		expression := strings.Join(methods, "|")
		if len(methods) > 1 {
			expression = "(" + expression + ")"
		}
		row := newPolicyRule("p", []string{model.GuestRole, rule.PathPattern, expression})
		row.Managed = true
		guestPolicies = append(guestPolicies, row)
	}
	allRules, err := permissionmanifest.Rules()
	if err != nil {
		return nil, err
	}
	catalog := make(map[string]map[string]struct{}, len(allRules))
	for _, rule := range allRules {
		methods := catalog[rule.PathPattern]
		if methods == nil {
			methods = map[string]struct{}{}
			catalog[rule.PathPattern] = methods
		}
		for _, method := range uniqueUpper(rule.Methods) {
			methods[method] = struct{}{}
		}
	}
	catalogEntries, err := permissionmanifest.Catalog()
	if err != nil {
		return nil, err
	}
	service := &Service{
		db:                db,
		roles:             roles,
		memberPolicies:    memberPolicies,
		guestPolicies:     guestPolicies,
		permissionCatalog: catalog,
		catalogEntries:    catalogEntries,
		log:               zap.NewNop(),
	}
	service.enforcer.Store(e)
	return service, nil
}

func newPolicyEnforcer(ctx context.Context, db *gorm.DB) (*casbin.SyncedEnforcer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m, err := casbinmodel.NewModelFromString(modelText)
	if err != nil {
		return nil, fmt.Errorf("parse casbin model: %w", err)
	}
	e, err := casbin.NewSyncedEnforcer(m, newGORMPolicyAdapter(db.WithContext(ctx)))
	if err != nil {
		return nil, fmt.Errorf("create casbin enforcer: %w", err)
	}
	return e, nil
}

// WithLogger attaches structured policy synchronization logging.
func (s *Service) WithLogger(log *zap.Logger) *Service {
	if log != nil {
		s.log = log
	}
	return s
}

func subject(id uint64) string { return "user:" + strconv.FormatUint(id, 10) }

// Enforce checks whether a user may call an API path and method.
func (s *Service) Enforce(ctx context.Context, userID uint64, path, method string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	ok, err := s.enforcer.Load().Enforce(subject(userID), path, method)
	if err != nil {
		return false, fmt.Errorf("enforce permission: %w", err)
	}
	return ok, nil
}

// PermissionCatalog returns the immutable generated permission directory.
func (s *Service) PermissionCatalog() []permissionmanifest.CatalogEntry {
	entries := make([]permissionmanifest.CatalogEntry, 0, len(s.catalogEntries))
	for _, entry := range s.catalogEntries {
		entry.Methods = append([]string{}, entry.Methods...)
		entries = append(entries, entry)
	}
	return entries
}

// EffectivePermissionCodes returns permission codes granted to a user by Casbin.
func (s *Service) EffectivePermissionCodes(ctx context.Context, userID uint64) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	codes := make([]string, 0, len(s.catalogEntries))
	seen := map[string]struct{}{}
	for _, entry := range s.catalogEntries {
		granted := false
		for _, method := range entry.Methods {
			allowed, err := s.Enforce(ctx, userID, entry.PathPattern, method)
			if err != nil {
				return nil, err
			}
			if allowed {
				granted = true
				break
			}
		}
		if !granted {
			continue
		}
		if _, exists := seen[entry.Code]; exists {
			continue
		}
		seen[entry.Code] = struct{}{}
		codes = append(codes, entry.Code)
	}
	sort.Strings(codes)
	return codes, nil
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
	if err := s.roles.UpdateDescription(ctx, r.ID, r.Description); err != nil {
		return nil, fmt.Errorf("update role: %w", err)
	}
	return r, nil
}

// DeleteRole removes an unused non-built-in role.
func (s *Service) DeleteRole(ctx context.Context, id uint64) error {
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		var role model.Role
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&role, id).Error; err != nil {
			return roleNotFound(err)
		}
		if role.Builtin {
			return apperror.New(http.StatusConflict, "builtin_role", "内置角色不能删除")
		}
		var count int64
		if err := tx.Model(&policyRule{}).Where("ptype = ? AND v1 = ?", "g", role.Name).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return apperror.New(http.StatusConflict, "role_in_use", "角色仍被用户使用")
		}
		if err := tx.Where("ptype = ? AND v0 = ?", "p", role.Name).Delete(&policyRule{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&role).Error; err != nil {
			return err
		}
		return recordPolicyChange(tx)
	})
	if err != nil {
		return err
	}
	return s.reloadAfterMutation(ctx)
}

// GetUserRoles returns direct roles for a user.
func (s *Service) GetUserRoles(ctx context.Context, userID uint64) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if idempotency.InTransaction(ctx) {
		rows := []policyRule{}
		if err := idempotency.DB(ctx, s.db).Where("ptype = ? AND v0 = ?", "g", subject(userID)).Find(&rows).Error; err != nil {
			return nil, err
		}
		roles := make([]string, 0, len(rows))
		for _, row := range rows {
			roles = append(roles, row.V1)
		}
		sort.Strings(roles)
		return roles, nil
	}
	roles, err := s.enforcer.Load().GetRolesForUser(subject(userID))
	sort.Strings(roles)
	return roles, err
}

// SetUserRoles replaces direct roles for a user.
func (s *Service) SetUserRoles(ctx context.Context, userID uint64, roles []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized := unique(roles)
	for _, role := range normalized {
		if role == model.GuestRole || role == model.MemberRole {
			return apperror.New(http.StatusBadRequest, "managed_base_role", "guest/member 基础角色只能由教务认证状态管理")
		}
	}
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := lockSuperAdminRole(tx); err != nil {
			return err
		}
		current := []policyRule{}
		if err := tx.Where("ptype = ? AND v0 = ?", "g", subject(userID)).Find(&current).Error; err != nil {
			return err
		}
		baseRole := model.GuestRole
		for _, row := range current {
			if row.V1 == model.MemberRole {
				baseRole = model.MemberRole
				break
			}
		}
		normalized = unique(append(normalized, baseRole))
		var count int64
		if err := tx.Model(&model.Role{}).Where("name IN ?", normalized).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(normalized)) {
			return apperror.New(http.StatusBadRequest, "unknown_role", "角色不存在")
		}
		wasSuperAdmin := false
		for _, row := range current {
			wasSuperAdmin = wasSuperAdmin || row.V1 == model.SuperAdminRole
		}
		if wasSuperAdmin && !contains(normalized, model.SuperAdminRole) {
			var remaining int64
			if err := tx.Table("casbin_rule AS c").
				Joins("JOIN users AS u ON c.v0 = CONCAT('user:', u.id)").
				Where("c.ptype = ? AND c.v1 = ? AND u.status = ? AND u.id <> ?", "g", model.SuperAdminRole, model.UserActive, userID).
				Count(&remaining).Error; err != nil {
				return err
			}
			if remaining == 0 {
				return apperror.New(http.StatusConflict, "last_super_admin", "不能移除最后一个超级管理员")
			}
		}
		if err := tx.Where("ptype = ? AND v0 = ?", "g", subject(userID)).Delete(&policyRule{}).Error; err != nil {
			return err
		}
		rows := make([]policyRule, 0, len(normalized))
		for _, name := range normalized {
			rows = append(rows, newPolicyRule("g", []string{subject(userID), name}))
		}
		if len(rows) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&model.User{}).Where("id = ?", userID).
			UpdateColumn("session_version", gorm.Expr("session_version + 1")).Error; err != nil {
			return err
		}
		return recordPolicyChange(tx)
	})
	if err != nil {
		return err
	}
	return s.reloadAfterMutation(ctx)
}

// GetPermissions returns API permissions assigned to a role.
func (s *Service) GetPermissions(ctx context.Context, roleID uint64) ([]Permission, error) {
	r, err := s.GetRole(ctx, roleID)
	if err != nil {
		return nil, err
	}
	if idempotency.InTransaction(ctx) {
		stored := []policyRule{}
		if err := idempotency.DB(ctx, s.db).Where("ptype = ? AND v0 = ?", "p", r.Name).Find(&stored).Error; err != nil {
			return nil, err
		}
		out := make([]Permission, 0, len(stored))
		for _, row := range stored {
			out = append(out, Permission{PathPattern: row.V1, Methods: strings.Split(strings.Trim(row.V2, "()"), "|")})
		}
		return out, nil
	}
	rows, err := s.enforcer.Load().GetPermissionsForUser(r.Name)
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
			allowedMethods := s.permissionCatalog[p.PathPattern]
			if _, ok := allowedMethods[m]; !ok {
				return apperror.New(400, "unknown_permission", "权限不在生成的 API 清单中")
			}
		}
		expr := methods[0]
		if len(methods) > 1 {
			expr = "(" + strings.Join(methods, "|") + ")"
		}
		rules = append(rules, []string{"", p.PathPattern, expr})
	}
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		var role model.Role
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&role, roleID).Error; err != nil {
			return roleNotFound(err)
		}
		if role.Builtin {
			return apperror.New(http.StatusConflict, "builtin_role", "内置角色权限不可修改")
		}
		var assignments []policyRule
		if err := tx.Where("ptype = ? AND v1 = ?", "g", role.Name).Find(&assignments).Error; err != nil {
			return err
		}
		if err := tx.Where("ptype = ? AND v0 = ?", "p", role.Name).Delete(&policyRule{}).Error; err != nil {
			return err
		}
		rows := make([]policyRule, 0, len(rules))
		for _, rule := range rules {
			rule[0] = role.Name
			rows = append(rows, newPolicyRule("p", rule))
		}
		if len(rows) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error; err != nil {
				return err
			}
		}
		userIDs := assignedUserIDs(assignments)
		if len(userIDs) > 0 {
			if err := tx.Model(&model.User{}).Where("id IN ?", userIDs).
				UpdateColumn("session_version", gorm.Expr("session_version + 1")).Error; err != nil {
				return err
			}
		}
		return recordPolicyChange(tx)
	})
	if err != nil {
		return err
	}
	return s.reloadAfterMutation(ctx)
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
	users, err := s.enforcer.Load().GetUsersForRole(model.SuperAdminRole)
	return len(users) > 1, err
}

// DisableUser atomically preserves one active super administrator while disabling a user.
func (s *Service) DisableUser(ctx context.Context, userID uint64) error {
	return idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := lockSuperAdminRole(tx); err != nil {
			return err
		}
		var targetSuperAdmin int64
		if err := tx.Model(&policyRule{}).
			Where("ptype = ? AND v0 = ? AND v1 = ?", "g", subject(userID), model.SuperAdminRole).
			Count(&targetSuperAdmin).Error; err != nil {
			return err
		}
		if targetSuperAdmin > 0 {
			var remaining int64
			err := tx.Table("users AS u").
				Joins("JOIN casbin_rule AS c ON c.v0 = CONCAT('user:', u.id)").
				Where("c.ptype = ? AND c.v1 = ? AND u.status = ? AND u.id <> ?", "g", model.SuperAdminRole, model.UserActive, userID).
				Count(&remaining).Error
			if err != nil {
				return err
			}
			if remaining == 0 {
				return apperror.New(http.StatusConflict, "last_super_admin", "不能禁用最后一个超级管理员")
			}
		}
		result := tx.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
			"status": model.UserDisabled, "session_version": gorm.Expr("session_version + 1"),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return apperror.New(http.StatusNotFound, "user_not_found", "用户不存在")
		}
		return nil
	})
}

// Bootstrap idempotently creates and assigns the built-in super administrator role.
func (s *Service) Bootstrap(ctx context.Context, userID uint64) error {
	if err := s.ensureBuiltinRole(ctx, model.GuestRole, "平台访客", s.guestPolicies); err != nil {
		return err
	}
	if err := s.EnsureCurrentBaseRoleForUser(ctx, userID); err != nil {
		return err
	}
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		var role model.Role
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("name = ?", model.SuperAdminRole).Take(&role).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			role = model.Role{Name: model.SuperAdminRole, Description: "系统超级管理员", Builtin: true}
			if err = tx.Create(&role).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		policy := newPolicyRule("p", []string{model.SuperAdminRole, "/api/v1/*", ".*"})
		policy.Managed = true
		grouping := newPolicyRule("g", []string{subject(userID), model.SuperAdminRole})
		if err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&[]policyRule{policy, grouping}).Error; err != nil {
			return err
		}
		return recordPolicyChange(tx)
	})
	if err != nil {
		return err
	}
	return s.reloadAfterMutation(ctx)
}

// ensureBuiltinRole creates a managed role and its generated policies without
// assigning it to a user. Base-role assignment remains state-driven.
func (s *Service) ensureBuiltinRole(ctx context.Context, name, description string, policies []policyRule) error {
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		var role model.Role
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("name = ?", name).Take(&role).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			if err = tx.Create(&model.Role{Name: name, Description: description, Builtin: true}).Error; err != nil {
				return err
			}
		case err != nil:
			return err
		case !role.Builtin:
			if err = tx.Model(&role).Update("builtin", true).Error; err != nil {
				return err
			}
		}
		if err = tx.Where("ptype = ? AND v0 = ? AND managed = ?", "p", name, true).Delete(&policyRule{}).Error; err != nil {
			return err
		}
		if len(policies) > 0 {
			if err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&policies).Error; err != nil {
				return err
			}
		}
		return recordPolicyChange(tx)
	})
	if err != nil {
		return err
	}
	return s.reloadAfterMutation(ctx)
}

// EnsureCurrentBaseRoleForUser reconciles policies without demoting a verified member.
func (s *Service) EnsureCurrentBaseRoleForUser(ctx context.Context, userID uint64) error {
	roles, err := s.GetUserRoles(ctx, userID)
	if err != nil {
		return err
	}
	if contains(roles, model.MemberRole) {
		return s.EnsureMemberForUser(ctx, userID)
	}
	return s.EnsureGuestForUser(ctx, userID)
}

// EnsureGuestForUser reconciles generated guest permissions and selects guest as the base role.
func (s *Service) EnsureGuestForUser(ctx context.Context, userID uint64) error {
	return s.EnsureGuestForUserTx(ctx, nil, userID)
}

// EnsureGuestForUserTx is the transaction-aware variant of EnsureGuestForUser.
// When tx is non-nil, all write SQL is performed through that transaction so
// callers can compose it with other writes (e.g. provisioning a brand-new
// account) without a window where the user exists but their role does not.
func (s *Service) EnsureGuestForUserTx(ctx context.Context, tx *gorm.DB, userID uint64) error {
	return s.ensureBaseRole(ctx, tx, userID, model.GuestRole, "平台访客", s.guestPolicies)
}

// EnsureMemberForUser reconciles generated member permissions and selects member as the base role.
func (s *Service) EnsureMemberForUser(ctx context.Context, userID uint64) error {
	return s.EnsureMemberForUserTx(ctx, nil, userID)
}

// EnsureMemberForUserTx is the transaction-aware variant of EnsureMemberForUser.
func (s *Service) EnsureMemberForUserTx(ctx context.Context, tx *gorm.DB, userID uint64) error {
	return s.ensureBaseRole(ctx, tx, userID, model.MemberRole, "已认证成员", s.memberPolicies)
}

// ensureBaseRoleReconciles and selects the named base role. The optional tx
// argument lets callers compose this operation with their own transaction;
// when nil a fresh transaction is opened.
func (s *Service) ensureBaseRole(
	ctx context.Context,
	tx *gorm.DB,
	userID uint64,
	roleName string,
	description string,
	managedPolicies []policyRule,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	run := func(handle *gorm.DB) error {
		var role model.Role
		err := handle.Clauses(clause.Locking{Strength: "UPDATE"}).Where("name = ?", roleName).Take(&role).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			role = model.Role{Name: roleName, Description: description, Builtin: true}
			if err = handle.Create(&role).Error; err != nil {
				return err
			}
		case err != nil:
			return err
		case !role.Builtin:
			if err = handle.Model(&role).Update("builtin", true).Error; err != nil {
				return err
			}
		}
		if err = handle.Where("ptype = ? AND v0 = ? AND managed = ?", "p", roleName, true).
			Delete(&policyRule{}).Error; err != nil {
			return err
		}
		if len(managedPolicies) > 0 {
			policies := append([]policyRule(nil), managedPolicies...)
			if err = handle.Clauses(clause.OnConflict{DoNothing: true}).Create(&policies).Error; err != nil {
				return err
			}
		}
		if err = handle.Where(
			"ptype = ? AND v0 = ? AND v1 IN ?",
			"g",
			subject(userID),
			[]string{model.GuestRole, model.MemberRole},
		).Delete(&policyRule{}).Error; err != nil {
			return err
		}
		grouping := newPolicyRule("g", []string{subject(userID), roleName})
		if err = handle.Clauses(clause.OnConflict{DoNothing: true}).Create(&grouping).Error; err != nil {
			return err
		}
		return recordPolicyChange(handle)
	}
	if tx != nil {
		return run(tx.WithContext(ctx))
	}
	if err := idempotency.DB(ctx, s.db).Transaction(run); err != nil {
		return err
	}
	return s.reloadAfterMutation(ctx)
}

func lockSuperAdminRole(tx *gorm.DB) error {
	var role model.Role
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("name = ?", model.SuperAdminRole).Take(&role).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

func roleNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperror.New(http.StatusNotFound, "role_not_found", "角色不存在")
	}
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

func assignedUserIDs(rows []policyRule) []uint64 {
	ids := make([]uint64, 0, len(rows))
	for _, row := range rows {
		value := strings.TrimPrefix(row.V0, "user:")
		id, err := strconv.ParseUint(value, 10, 64)
		if err == nil && id != 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Service) reloadAfterMutation(ctx context.Context) error {
	return idempotency.DeferAfterCommit(ctx, func(callbackContext context.Context) error {
		return s.reloadPolicy(callbackContext)
	})
}
