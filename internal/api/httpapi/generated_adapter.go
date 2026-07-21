package httpapi

import (
	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/api/generated"
)

// Live handles the generated liveness operation.
func (h *Handler) Live(c *gin.Context) { success(c, 200, gin.H{"status": "ok"}) }

// Ready handles the generated readiness operation.
func (h *Handler) Ready(c *gin.Context) { h.ready(c) }

// Login handles the generated login operation.
func (h *Handler) Login(c *gin.Context) { h.login(c) }

// Refresh handles the generated token refresh operation.
func (h *Handler) Refresh(c *gin.Context) { h.refresh(c) }

// Logout handles the generated logout operation.
func (h *Handler) Logout(c *gin.Context) { h.logout(c) }

// GetMe handles the generated current-user operation.
func (h *Handler) GetMe(c *gin.Context) { h.me(c) }

// ListUsers handles the generated user-list operation.
func (h *Handler) ListUsers(c *gin.Context, _ generated.ListUsersParams) { h.listUsers(c) }

// CreateUser handles the generated user-create operation.
func (h *Handler) CreateUser(c *gin.Context, _ generated.CreateUserParams) {
	h.idempotent(c, "CreateUser", func() { h.createUser(c) })
}

// GetUser handles the generated user-detail operation.
func (h *Handler) GetUser(c *gin.Context, _ generated.ID) { h.getUser(c) }

// UpdateUser handles the generated user-update operation.
func (h *Handler) UpdateUser(c *gin.Context, _ generated.ID, _ generated.UpdateUserParams) {
	h.idempotent(c, "UpdateUser", func() { h.updateUser(c) })
}

// SetUserStatus handles the generated user-status operation.
func (h *Handler) SetUserStatus(c *gin.Context, _ generated.ID, _ generated.SetUserStatusParams) {
	h.idempotent(c, "SetUserStatus", func() { h.setUserStatus(c) })
}

// GetUserRoles handles the generated user-role query operation.
func (h *Handler) GetUserRoles(c *gin.Context, _ generated.ID) { h.getUserRoles(c) }

// SetUserRoles handles the generated user-role update operation.
func (h *Handler) SetUserRoles(c *gin.Context, _ generated.ID, _ generated.SetUserRolesParams) {
	h.idempotent(c, "SetUserRoles", func() { h.setUserRoles(c) })
}

// ListRoles handles the generated role-list operation.
func (h *Handler) ListRoles(c *gin.Context, _ generated.ListRolesParams) { h.listRoles(c) }

// CreateRole handles the generated role-create operation.
func (h *Handler) CreateRole(c *gin.Context, _ generated.CreateRoleParams) {
	h.idempotent(c, "CreateRole", func() { h.createRole(c) })
}

// UpdateRole handles the generated role-update operation.
func (h *Handler) UpdateRole(c *gin.Context, _ generated.ID, _ generated.UpdateRoleParams) {
	h.idempotent(c, "UpdateRole", func() { h.updateRole(c) })
}

// DeleteRole handles the generated role-delete operation.
func (h *Handler) DeleteRole(c *gin.Context, _ generated.ID, _ generated.DeleteRoleParams) {
	h.idempotent(c, "DeleteRole", func() { h.deleteRole(c) })
}

// GetPermissions handles the generated permission query operation.
func (h *Handler) GetPermissions(c *gin.Context, _ generated.ID) { h.getPermissions(c) }

// SetPermissions handles the generated permission update operation.
func (h *Handler) SetPermissions(c *gin.Context, _ generated.ID, _ generated.SetPermissionsParams) {
	h.idempotent(c, "SetPermissions", func() { h.setPermissions(c) })
}

// ListConfigs handles the generated configuration-list operation.
func (h *Handler) ListConfigs(c *gin.Context, _ generated.ListConfigsParams) { h.listConfigs(c) }

// CreateConfig handles the generated configuration-create operation.
func (h *Handler) CreateConfig(c *gin.Context, _ generated.CreateConfigParams) {
	h.idempotent(c, "CreateConfig", func() { h.createConfig(c) })
}

// GetConfig handles the generated configuration-detail operation.
func (h *Handler) GetConfig(c *gin.Context, _ generated.ID) { h.getConfig(c) }

// UpdateConfig handles the generated configuration-update operation.
func (h *Handler) UpdateConfig(c *gin.Context, _ generated.ID, _ generated.UpdateConfigParams) {
	h.idempotent(c, "UpdateConfig", func() { h.updateConfig(c) })
}

// DeleteConfig handles the generated configuration-delete operation.
func (h *Handler) DeleteConfig(c *gin.Context, _ generated.ID, _ generated.DeleteConfigParams) {
	h.idempotent(c, "DeleteConfig", func() { h.deleteConfig(c) })
}

var _ generated.ServerInterface = (*Handler)(nil)
