package httpapi

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	ginmiddleware "github.com/oapi-codegen/gin-middleware"
	"github.com/weouc-plus/campus-platform/internal/api/generated"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/auth"
	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/core/permission"
	"github.com/weouc-plus/campus-platform/internal/core/user"
	"go.uber.org/zap"
)

// Handler wires core services to HTTP.
type Handler struct {
	auth        *auth.Service
	users       *user.Service
	permissions *permission.Service
	configs     *configcenter.Service
	mysql       func(context.Context) error
	redis       func(context.Context) error
	log         *zap.Logger
}

// New creates an HTTP handler backed by the supplied core services.
func New(authService *auth.Service, userService *user.Service, permissionService *permission.Service, configService *configcenter.Service, mysqlPing, redisPing func(context.Context) error, log *zap.Logger) *Handler {
	return &Handler{auth: authService, users: userService, permissions: permissionService, configs: configService, mysql: mysqlPing, redis: redisPing, log: log}
}

// Router creates the Gin engine and registers all routes.
func (h *Handler) Router() (*gin.Engine, error) {
	swagger, err := generated.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("load generated OpenAPI contract: %w", err)
	}
	// Host validation is deployment-specific; paths and payloads remain validated.
	swagger.Servers = nil
	r := gin.New()
	validator := ginmiddleware.OapiRequestValidatorWithOptions(swagger, &ginmiddleware.Options{
		Options: openapi3filter.Options{AuthenticationFunc: func(context.Context, *openapi3filter.AuthenticationInput) error { return nil }},
		ErrorHandler: func(c *gin.Context, _ string, status int) {
			failure(c, apperror.New(status, "invalid_request", "请求不符合 API 契约"))
		},
	})
	r.Use(requestID(), recovery(h.log), accessLog(h.log), validator, h.security())
	generated.RegisterHandlersWithOptions(r, h, generated.GinServerOptions{ErrorHandler: func(c *gin.Context, err error, status int) {
		failure(c, apperror.Wrap(status, "invalid_parameter", "路径参数无效", err))
	}})
	return r, nil
}
func (h *Handler) ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2e9)
	defer cancel()
	if err := h.mysql(ctx); err != nil {
		failure(c, apperror.Wrap(503, "not_ready", "服务尚未就绪", err))
		return
	}
	if err := h.redis(ctx); err != nil {
		failure(c, apperror.Wrap(503, "not_ready", "服务尚未就绪", err))
		return
	}
	success(c, 200, gin.H{"status": "ready"})
}

func bind(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		failure(c, apperror.Wrap(400, "invalid_request", "请求参数无效", err))
		return false
	}
	return true
}

func idParam(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		failure(c, apperror.Wrap(400, "invalid_id", "资源 ID 无效", err))
		return 0, false
	}
	return id, true
}
func paging(c *gin.Context) (int, int) {
	p, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	s, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if p < 1 {
		p = 1
	}
	if s < 1 {
		s = 20
	}
	if s > 100 {
		s = 100
	}
	return p, s
}

func (h *Handler) login(c *gin.Context) {
	var req generated.LoginRequest
	if !bind(c, &req) {
		return
	}
	pair, err := h.auth.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, pair)
}
func (h *Handler) refresh(c *gin.Context) {
	var req generated.RefreshRequest
	if !bind(c, &req) {
		return
	}
	pair, err := h.auth.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, pair)
}
func (h *Handler) logout(c *gin.Context) {
	if err := h.auth.Logout(c.Request.Context(), c.GetString(sessionIDKey)); err != nil {
		failure(c, err)
		return
	}
	success(c, 200, gin.H{"logged_out": true})
}
func (h *Handler) me(c *gin.Context) {
	uid := c.GetUint64(userIDKey)
	u, err := h.users.Get(c.Request.Context(), uid)
	if err != nil {
		failure(c, err)
		return
	}
	roles, err := h.permissions.GetUserRoles(c.Request.Context(), uid)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, gin.H{"user": u, "roles": roles})
}
func (h *Handler) listUsers(c *gin.Context) {
	p, s := paging(c)
	rows, total, err := h.users.List(c.Request.Context(), p, s)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, pageData(rows, p, s, total))
}
func (h *Handler) createUser(c *gin.Context) {
	var req generated.CreateUserRequest
	if !bind(c, &req) {
		return
	}
	u, err := h.users.Create(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 201, u)
}
func (h *Handler) getUser(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	u, err := h.users.Get(c.Request.Context(), id)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, u)
}
func (h *Handler) updateUser(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req generated.UpdateUserRequest
	if !bind(c, &req) {
		return
	}
	u, err := h.users.Update(c.Request.Context(), id, req.Username, req.Password)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, u)
}
func (h *Handler) setUserStatus(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req generated.UserStatusRequest
	if !bind(c, &req) {
		return
	}
	u, err := h.users.SetStatus(c.Request.Context(), id, string(req.Status))
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, u)
}
func (h *Handler) getUserRoles(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if _, err := h.users.Get(c.Request.Context(), id); err != nil {
		failure(c, err)
		return
	}
	roles, err := h.permissions.GetUserRoles(c.Request.Context(), id)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, gin.H{"roles": roles})
}
func (h *Handler) setUserRoles(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if _, err := h.users.Get(c.Request.Context(), id); err != nil {
		failure(c, err)
		return
	}
	var req generated.UserRolesRequest
	if !bind(c, &req) {
		return
	}
	if err := h.permissions.SetUserRoles(c.Request.Context(), id, req.Roles); err != nil {
		failure(c, err)
		return
	}
	h.getUserRoles(c)
}
func (h *Handler) listRoles(c *gin.Context) {
	p, s := paging(c)
	rows, total, err := h.permissions.ListRoles(c.Request.Context(), p, s)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, pageData(rows, p, s, total))
}
func (h *Handler) createRole(c *gin.Context) {
	var req generated.CreateRoleRequest
	if !bind(c, &req) {
		return
	}
	description := ""
	if req.Description != nil {
		description = *req.Description
	}
	r, err := h.permissions.CreateRole(c.Request.Context(), req.Name, description, false)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 201, r)
}
func (h *Handler) updateRole(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req generated.UpdateRoleRequest
	if !bind(c, &req) {
		return
	}
	r, err := h.permissions.UpdateRole(c.Request.Context(), id, req.Description)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, r)
}
func (h *Handler) deleteRole(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := h.permissions.DeleteRole(c.Request.Context(), id); err != nil {
		failure(c, err)
		return
	}
	success(c, 200, gin.H{"deleted": true})
}
func (h *Handler) getPermissions(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	rows, err := h.permissions.GetPermissions(c.Request.Context(), id)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, gin.H{"permissions": rows})
}
func (h *Handler) setPermissions(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req generated.PermissionsRequest
	if !bind(c, &req) {
		return
	}
	permissions := make([]permission.Permission, 0, len(req.Permissions))
	for _, p := range req.Permissions {
		permissions = append(permissions, permission.Permission{PathPattern: p.PathPattern, Methods: p.Methods})
	}
	if err := h.permissions.SetPermissions(c.Request.Context(), id, permissions); err != nil {
		failure(c, err)
		return
	}
	h.getPermissions(c)
}
func (h *Handler) listConfigs(c *gin.Context) {
	p, s := paging(c)
	rows, total, err := h.configs.List(c.Request.Context(), strings.TrimSpace(c.Query("group")), p, s)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, pageData(rows, p, s, total))
}
func (h *Handler) createConfig(c *gin.Context) {
	var req generated.CreateConfigRequest
	if !bind(c, &req) {
		return
	}
	encrypted := false
	if req.Encrypted != nil {
		encrypted = *req.Encrypted
	}
	v, err := h.configs.Create(c.Request.Context(), req.Group, req.Key, req.Value, encrypted, c.GetUint64(userIDKey))
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 201, v)
}
func (h *Handler) getConfig(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	v, err := h.configs.Get(c.Request.Context(), id)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, v)
}
func (h *Handler) updateConfig(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req generated.UpdateConfigRequest
	if !bind(c, &req) {
		return
	}
	v, err := h.configs.Update(c.Request.Context(), id, req.ExpectedVersion, req.Value, c.GetUint64(userIDKey))
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, v)
}
func (h *Handler) deleteConfig(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := h.configs.Delete(c.Request.Context(), id); err != nil {
		failure(c, err)
		return
	}
	success(c, 200, gin.H{"deleted": true})
}
