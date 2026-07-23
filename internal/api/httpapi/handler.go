package httpapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	ginmiddleware "github.com/oapi-codegen/gin-middleware"
	"github.com/weouc-plus/campus-platform/internal/api/generated"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/auth"
	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	"github.com/weouc-plus/campus-platform/internal/core/permission"
	"github.com/weouc-plus/campus-platform/internal/core/user"
	academicapp "github.com/weouc-plus/campus-platform/internal/modules/academic_verification/application"
	activityapp "github.com/weouc-plus/campus-platform/internal/modules/activity/application"
	carpoolapp "github.com/weouc-plus/campus-platform/internal/modules/carpool/application"
	errandapp "github.com/weouc-plus/campus-platform/internal/modules/errand/application"
	marketplaceapp "github.com/weouc-plus/campus-platform/internal/modules/marketplace/application"
	noticeapp "github.com/weouc-plus/campus-platform/internal/modules/notice/application"
	tradeapp "github.com/weouc-plus/campus-platform/internal/modules/trade/application"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Handler wires core services to HTTP.
type Handler struct {
	auth              *auth.Service
	users             *user.Service
	permissions       *permission.Service
	configs           *configcenter.Service
	notices           *noticeapp.Manager
	activities        *activityapp.Manager
	marketplace       *marketplaceapp.Manager
	errands           *errandapp.Manager
	carpools          *carpoolapp.Manager
	trades            *tradeapp.Manager
	academic          *academicapp.Manager
	academicGate      AcademicVerificationGate
	mysql             func(context.Context) error
	redis             func(context.Context) error
	log               *zap.Logger
	maxBodyBytes      int64
	maxHeaderBytes    int
	readinessMu       sync.Mutex
	readinessAt       time.Time
	readinessErr      error
	authLimiter       AuthLimiter
	db                *gorm.DB
	trustedProxyCIDRs []string
	trustedProxyNets  []*net.IPNet
	requireProxyHTTPS bool
}

// AuthLimiter is the distributed brute-force boundary used by auth endpoints.
type AuthLimiter interface {
	AllowLoginIP(context.Context, string) (bool, error)
	AllowWeChatLogin(context.Context, string) (bool, error)
	RecordLoginFailure(context.Context, string) (bool, error)
	ClearLoginFailures(context.Context, string) error
	AllowRefresh(context.Context, string, string) (bool, error)
}

// WithNotices attaches the optional notification-center module.
func (h *Handler) WithNotices(manager *noticeapp.Manager) *Handler { h.notices = manager; return h }

// WithActivities attaches the activity domain service.
func (h *Handler) WithActivities(manager *activityapp.Manager) *Handler {
	h.activities = manager
	return h
}

// WithMarketplace attaches the marketplace domain service.
func (h *Handler) WithMarketplace(manager *marketplaceapp.Manager) *Handler {
	h.marketplace = manager
	return h
}

// WithErrands attaches the errand fulfillment service.
func (h *Handler) WithErrands(manager *errandapp.Manager) *Handler {
	h.errands = manager
	return h
}

// WithCarpools attaches the carpool domain service.
func (h *Handler) WithCarpools(manager *carpoolapp.Manager) *Handler {
	h.carpools = manager
	return h
}

// WithTrades attaches participant-scoped trade order queries.
func (h *Handler) WithTrades(manager *tradeapp.Manager) *Handler { h.trades = manager; return h }

// WithAcademicVerification attaches academic identity verification and its runtime write gate.
func (h *Handler) WithAcademicVerification(manager *academicapp.Manager) *Handler {
	h.academic = manager
	h.academicGate = manager
	return h
}

// AcademicVerificationGate is the narrow runtime boundary used before business writes.
type AcademicVerificationGate interface {
	IsVerified(context.Context, uint64) (bool, error)
}

const (
	academicVerificationStatusKey = "academic_verification_status"
	actionVerifyAcademic          = "verify_academic"
)

// WithAcademicVerificationGate overrides only the write gate, primarily for isolated adapters.
func (h *Handler) WithAcademicVerificationGate(gate AcademicVerificationGate) *Handler {
	h.academicGate = gate
	return h
}

func (h *Handler) requireAcademicVerification(c *gin.Context) bool {
	if h.academicGate == nil {
		failure(c, apperror.New(http.StatusServiceUnavailable, "academic_verification_unavailable", "教务认证服务暂不可用"))
		return false
	}
	verified, err := h.academicGate.IsVerified(c.Request.Context(), c.GetUint64(userIDKey))
	if err != nil {
		failure(c, err)
		return false
	}
	if !verified {
		failure(c, apperror.New(http.StatusForbidden, "academic_verification_required", "完成教务认证后才能执行此操作"))
		return false
	}
	return true
}

// availableActionsForViewer replaces one action that requires academic
// verification with the action that starts verification. Publisher-owned and
// participant-owned actions pass through unchanged because they do not include
// the restricted action.
func (h *Handler) availableActionsForViewer(
	c *gin.Context,
	actions []string,
	restrictedAction string,
) ([]string, error) {
	if actions == nil {
		actions = []string{}
	}
	if c.GetUint64(userIDKey) == 0 || !containsAction(actions, restrictedAction) {
		return actions, nil
	}
	verified, err := h.viewerAcademicVerified(c)
	if err != nil {
		return nil, err
	}
	if verified {
		return actions, nil
	}
	return []string{actionVerifyAcademic}, nil
}

func (h *Handler) viewerAcademicVerified(c *gin.Context) (bool, error) {
	if cached, ok := c.Get(academicVerificationStatusKey); ok {
		verified, ok := cached.(bool)
		if ok {
			return verified, nil
		}
	}
	if h.academicGate == nil {
		return false, apperror.New(
			http.StatusServiceUnavailable,
			"academic_verification_unavailable",
			"教务认证服务暂不可用",
		)
	}
	verified, err := h.academicGate.IsVerified(c.Request.Context(), c.GetUint64(userIDKey))
	if err != nil {
		return false, err
	}
	c.Set(academicVerificationStatusKey, verified)
	return verified, nil
}

func containsAction(actions []string, target string) bool {
	for _, action := range actions {
		if action == target {
			return true
		}
	}
	return false
}

func generatedActions[T ~string](actions []string) []T {
	result := make([]T, len(actions))
	for i := range actions {
		result[i] = T(actions[i])
	}
	return result
}

// New creates an HTTP handler backed by the supplied core services.
func New(authService *auth.Service, userService *user.Service, permissionService *permission.Service, configService *configcenter.Service, mysqlPing, redisPing func(context.Context) error, log *zap.Logger) *Handler {
	return &Handler{
		auth: authService, users: userService, permissions: permissionService, configs: configService,
		mysql: mysqlPing, redis: redisPing, log: log,
		maxBodyBytes: 1 << 20, maxHeaderBytes: 64 << 10,
	}
}

// WithRequestLimits sets pre-parser HTTP body and aggregate-header limits.
func (h *Handler) WithRequestLimits(maxBodyBytes int64, maxHeaderBytes int) *Handler {
	if maxBodyBytes > 0 {
		h.maxBodyBytes = maxBodyBytes
	}
	if maxHeaderBytes > 0 {
		h.maxHeaderBytes = maxHeaderBytes
	}
	return h
}

// WithAuthLimiter attaches the shared Redis authentication limiter.
func (h *Handler) WithAuthLimiter(limiter AuthLimiter) *Handler {
	h.authLimiter = limiter
	return h
}

// WithProxyPolicy configures the only peers allowed to supply client IP and HTTPS metadata.
func (h *Handler) WithProxyPolicy(trustedCIDRs []string, requireHTTPS bool) *Handler {
	h.trustedProxyCIDRs = append([]string(nil), trustedCIDRs...)
	h.requireProxyHTTPS = requireHTTPS
	return h
}

// WithDatabase attaches the transaction boundary used by idempotent writes.
func (h *Handler) WithDatabase(db *gorm.DB) *Handler {
	h.db = db
	return h
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
	h.trustedProxyNets = make([]*net.IPNet, 0, len(h.trustedProxyCIDRs))
	for _, cidr := range h.trustedProxyCIDRs {
		_, network, parseErr := net.ParseCIDR(cidr)
		if parseErr != nil {
			return nil, fmt.Errorf("parse trusted proxy CIDR %q: %w", cidr, parseErr)
		}
		h.trustedProxyNets = append(h.trustedProxyNets, network)
	}
	if err = r.SetTrustedProxies(h.trustedProxyCIDRs); err != nil {
		return nil, fmt.Errorf("configure trusted proxies: %w", err)
	}
	validator := ginmiddleware.OapiRequestValidatorWithOptions(swagger, &ginmiddleware.Options{
		Options: openapi3filter.Options{AuthenticationFunc: func(context.Context, *openapi3filter.AuthenticationInput) error { return nil }},
		ErrorHandler: func(c *gin.Context, _ string, status int) {
			failure(c, apperror.New(status, "invalid_request", "请求不符合 API 契约"))
		},
	})
	r.Use(requestID(), requestLimits(h.maxBodyBytes, h.maxHeaderBytes), recovery(h.log), accessLog(h.log), securityHeaders(), h.proxyBoundary(), h.security(), validator)
	generated.RegisterHandlersWithOptions(r, h, generated.GinServerOptions{ErrorHandler: func(c *gin.Context, err error, status int) {
		failure(c, apperror.Wrap(status, "invalid_parameter", "路径参数无效", err))
	}})
	return r, nil
}
func (h *Handler) ready(c *gin.Context) {
	h.readinessMu.Lock()
	if time.Since(h.readinessAt) < 5*time.Second {
		err := h.readinessErr
		h.readinessMu.Unlock()
		if err != nil {
			failure(c, apperror.Wrap(503, "not_ready", "服务尚未就绪", err))
			return
		}
		success(c, 200, gin.H{"status": "ready"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2e9)
	defer cancel()
	err := h.mysql(ctx)
	if err == nil {
		err = h.redis(ctx)
	}
	h.readinessAt = time.Now()
	h.readinessErr = err
	h.readinessMu.Unlock()
	if err != nil {
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

func setGeneratedPathParam(c *gin.Context, name string, value uint64) {
	c.Set("generated.path."+name, value)
}

func setGeneratedParams(c *gin.Context, operationID string, params any) {
	c.Set("generated.params."+operationID, params)
}

func generatedParams[T any](c *gin.Context, operationID string) (T, bool) {
	value, exists := c.Get("generated.params." + operationID)
	if !exists {
		var zero T
		return zero, false
	}
	params, ok := value.(T)
	return params, ok
}

func (h *Handler) login(c *gin.Context) {
	var req generated.LoginRequest
	if !bind(c, &req) {
		return
	}
	if !h.allowLogin(c) {
		return
	}
	pair, err := h.auth.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		if appErr, ok := apperror.As(err); ok && appErr.Code == "invalid_credentials" {
			if !h.recordLoginFailure(c, req.Username) {
				return
			}
		}
		failure(c, err)
		return
	}
	h.clearLoginFailures(c, req.Username)
	c.Header("Cache-Control", "no-store, private")
	c.Header("Pragma", "no-cache")
	success(c, 200, pair)
}
func (h *Handler) refresh(c *gin.Context) {
	var req generated.RefreshRequest
	if !bind(c, &req) {
		return
	}
	if !h.allowRefresh(c, req.RefreshToken) {
		return
	}
	pair, err := h.auth.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		failure(c, err)
		return
	}
	c.Header("Cache-Control", "no-store, private")
	c.Header("Pragma", "no-cache")
	success(c, 200, pair)
}

func (h *Handler) wechatLogin(c *gin.Context) {
	var req generated.WechatLoginRequest
	if !bind(c, &req) {
		return
	}
	if !h.allowWeChatLogin(c) {
		return
	}
	pair, err := h.auth.LoginByWeChat(c.Request.Context(), req.AppID, req.Code)
	if err != nil {
		if appErr, ok := apperror.As(err); ok && appErr.Code == "invalid_wechat_code" {
			if !h.recordLoginFailure(c, "wechat:"+req.AppID) {
				return
			}
		}
		failure(c, err)
		return
	}
	c.Header("Cache-Control", "no-store, private")
	c.Header("Pragma", "no-cache")
	success(c, 200, pair)
}

func (h *Handler) allowLogin(c *gin.Context) bool {
	if h.authLimiter == nil {
		return true
	}
	allowed, err := h.authLimiter.AllowLoginIP(c.Request.Context(), h.clientIP(c.Request))
	return h.handleRateLimit(c, allowed, err)
}

func (h *Handler) allowWeChatLogin(c *gin.Context) bool {
	if h.authLimiter == nil {
		return true
	}
	allowed, err := h.authLimiter.AllowWeChatLogin(c.Request.Context(), h.clientIP(c.Request))
	return h.handleRateLimit(c, allowed, err)
}

func (h *Handler) recordLoginFailure(c *gin.Context, username string) bool {
	if h.authLimiter == nil {
		return true
	}
	allowed, err := h.authLimiter.RecordLoginFailure(c.Request.Context(), username)
	return h.handleRateLimit(c, allowed, err)
}

func (h *Handler) clearLoginFailures(c *gin.Context, username string) {
	if h.authLimiter == nil {
		return
	}
	if err := h.authLimiter.ClearLoginFailures(c.Request.Context(), username); err != nil {
		h.log.Warn("clear login failure limiter", zap.Error(err), zap.String("request_id", c.GetString(requestIDKey)))
	}
}

func (h *Handler) allowRefresh(c *gin.Context, token string) bool {
	if h.authLimiter == nil {
		return true
	}
	allowed, err := h.authLimiter.AllowRefresh(c.Request.Context(), h.auth.RefreshFamily(token), h.clientIP(c.Request))
	return h.handleRateLimit(c, allowed, err)
}

func (h *Handler) handleRateLimit(c *gin.Context, allowed bool, err error) bool {
	if err != nil {
		failure(c, apperror.Wrap(http.StatusServiceUnavailable, "auth_limiter_unavailable", "认证服务暂时不可用", err))
		return false
	}
	if !allowed {
		c.Header("Retry-After", "60")
		failure(c, apperror.New(http.StatusTooManyRequests, "rate_limited", "请求过于频繁，请稍后重试"))
		return false
	}
	return true
}

func peerIP(request *http.Request) net.IP {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	return net.ParseIP(strings.TrimSpace(host))
}

func (h *Handler) clientIP(request *http.Request) string {
	peer := peerIP(request)
	if peer == nil {
		return request.RemoteAddr
	}
	if !h.isTrustedProxy(peer) {
		return peer.String()
	}
	current := peer
	forwarded := strings.Split(request.Header.Get("X-Forwarded-For"), ",")
	for index := len(forwarded) - 1; index >= 0 && h.isTrustedProxy(current); index-- {
		candidate := net.ParseIP(strings.TrimSpace(forwarded[index]))
		if candidate == nil {
			return peer.String()
		}
		current = candidate
	}
	return current.String()
}

func (h *Handler) isTrustedProxy(ip net.IP) bool {
	for _, network := range h.trustedProxyNets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func (h *Handler) proxyBoundary() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !h.requireProxyHTTPS {
			c.Next()
			return
		}
		peer := peerIP(c.Request)
		if peer == nil || !h.isTrustedProxy(peer) || forwardedProto(c.Request) != "https" {
			failure(c, apperror.New(http.StatusForbidden, "secure_proxy_required", "请求必须通过受信任的 HTTPS 代理"))
			c.Abort()
			return
		}
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Next()
	}
}

func forwardedProto(request *http.Request) string {
	values := strings.Split(request.Header.Get("X-Forwarded-Proto"), ",")
	return strings.ToLower(strings.TrimSpace(values[len(values)-1]))
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
	permissionCodes, err := h.permissions.EffectivePermissionCodes(c.Request.Context(), uid)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, gin.H{"user": u, "roles": roles, "permissions": permissionCodes})
}

func (h *Handler) changeMyPassword(c *gin.Context) {
	var request generated.ChangeMyPasswordRequest
	if !bind(c, &request) {
		return
	}
	if err := h.users.ChangePassword(
		c.Request.Context(),
		c.GetUint64(userIDKey),
		request.CurrentPassword,
		request.NewPassword,
	); err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, gin.H{"password_changed": true, "reauthentication_required": true})
}

func (h *Handler) listPermissionCatalog(c *gin.Context) {
	success(c, 200, gin.H{"permissions": h.permissions.PermissionCatalog()})
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
	if err := idempotency.DeferAfterCommit(c.Request.Context(), func(callbackContext context.Context) error {
		return h.auth.RevokeUser(callbackContext, id)
	}); err != nil {
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
	rows, total, err := h.configs.List(c.Request.Context(), strings.TrimSpace(c.Query("group")), strings.TrimSpace(c.Query("keyword")), strings.TrimSpace(c.Query("format")), strings.TrimSpace(c.Query("visibility")), p, s)
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
	format, visibility := "string", "admin"
	if req.Format != nil {
		format = string(*req.Format)
	}
	if req.Visibility != nil {
		visibility = string(*req.Visibility)
	}
	v, err := h.configs.Create(c.Request.Context(), req.Group, req.Key, req.Value, format, visibility, encrypted, c.GetUint64(userIDKey))
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
	var visibility *string
	if req.Visibility != nil {
		value := string(*req.Visibility)
		visibility = &value
	}
	v, err := h.configs.Update(c.Request.Context(), id, req.ExpectedVersion, req.Value, visibility, c.GetUint64(userIDKey))
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
	params, ok := generatedParams[generated.DeleteConfigParams](c, "DeleteConfig")
	if !ok {
		failure(c, apperror.New(400, "invalid_parameter", "缺少已校验的配置删除参数"))
		return
	}
	if err := h.configs.Delete(c.Request.Context(), id, uint64(params.ExpectedVersion)); err != nil {
		failure(c, err)
		return
	}
	success(c, 200, gin.H{"deleted": true})
}

func (h *Handler) getRuntimeConfig(c *gin.Context) {
	group, key := c.Param("group"), c.Param("key")
	v, id, err := h.configs.Runtime(c.Request.Context(), group, key)
	if err != nil {
		failure(c, err)
		return
	}
	etag := fmt.Sprintf("\"config-%d-v%d\"", id, v.Version)
	if c.GetHeader("If-None-Match") == etag {
		c.Header("ETag", etag)
		c.Status(http.StatusNotModified)
		return
	}
	c.Header("ETag", etag)
	c.Header("Cache-Control", "public, max-age=0, must-revalidate")
	success(c, 200, v)
}
