// Package app wires the platform components.
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/weouc-plus/campus-platform/internal/api/httpapi"
	"github.com/weouc-plus/campus-platform/internal/core/auth"
	"github.com/weouc-plus/campus-platform/internal/core/auth/wechat"
	"github.com/weouc-plus/campus-platform/internal/core/bootstrap"
	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/core/permission"
	"github.com/weouc-plus/campus-platform/internal/core/user"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/logger"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/mysql"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/redisclient"
	academicapp "github.com/weouc-plus/campus-platform/internal/modules/academic_verification/application"
	academicinfra "github.com/weouc-plus/campus-platform/internal/modules/academic_verification/infrastructure"
	activityapp "github.com/weouc-plus/campus-platform/internal/modules/activity/application"
	activityinfra "github.com/weouc-plus/campus-platform/internal/modules/activity/infrastructure"
	carpoolapp "github.com/weouc-plus/campus-platform/internal/modules/carpool/application"
	carpoolinfra "github.com/weouc-plus/campus-platform/internal/modules/carpool/infrastructure"
	errandapp "github.com/weouc-plus/campus-platform/internal/modules/errand/application"
	errandinfra "github.com/weouc-plus/campus-platform/internal/modules/errand/infrastructure"
	marketplaceapp "github.com/weouc-plus/campus-platform/internal/modules/marketplace/application"
	marketplaceinfra "github.com/weouc-plus/campus-platform/internal/modules/marketplace/infrastructure"
	noticeapp "github.com/weouc-plus/campus-platform/internal/modules/notice/application"
	noticeinfra "github.com/weouc-plus/campus-platform/internal/modules/notice/infrastructure"
	tradeapp "github.com/weouc-plus/campus-platform/internal/modules/trade/application"
	tradeinfra "github.com/weouc-plus/campus-platform/internal/modules/trade/infrastructure"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Runtime owns initialized dependencies and the HTTP router.
type Runtime struct {
	Config      bootstrap.Config
	DB          *gorm.DB
	Redis       *redis.Client
	Logger      *zap.Logger
	Router      *gin.Engine
	Users       *user.Service
	Permissions *permission.Service
	Notices     *noticeapp.Manager
	Activities  *activityapp.Manager
	Marketplace *marketplaceapp.Manager
	Errands     *errandapp.Manager
	Carpools    *carpoolapp.Manager
	Trades      *tradeapp.Manager
	Academic    *academicapp.Manager
	WeChat      *wechat.CachingResolver
}

// Build initializes all runtime dependencies.
func Build(ctx context.Context, cfg bootstrap.Config) (*Runtime, error) {
	log, err := logger.New()
	if err != nil {
		return nil, fmt.Errorf("create logger: %w", err)
	}
	runtime := &Runtime{Config: cfg, Logger: log}
	initialized := false
	defer func() {
		if !initialized {
			_ = runtime.Close()
		}
	}()
	db, err := mysql.Open(ctx, cfg.MySQL.DSN)
	if err != nil {
		return nil, err
	}
	runtime.DB = db
	rdb, err := redisclient.Open(ctx, cfg.Redis, cfg.Redis.DB)
	if err != nil {
		return nil, err
	}
	runtime.Redis = rdb
	userRepo := mysql.NewUserRepository(db)
	roleRepo := mysql.NewRoleRepository(db)
	permissionService, err := permission.NewService(ctx, db, roleRepo)
	if err != nil {
		return nil, err
	}
	permissionService.WithLogger(log)
	cipher, err := configcenter.NewCipher(cfg.Secret.ConfigKey)
	if err != nil {
		return nil, err
	}
	configService := configcenter.NewService(mysql.NewConfigRepository(db), cipher)
	userService := user.NewService(userRepo, permissionService)
	wechatResolver, err := wechat.NewCachingResolver(ctx, configService, "wechat", 5*time.Minute, log)
	if err != nil {
		return nil, fmt.Errorf("init wechat resolver: %w", err)
	}
	wechatResolver.Start(ctx)
	wechatClient := wechat.NewHTTPClient(cfg.WeChat.Endpoint, cfg.WeChat.HTTPTimeout, wechatResolver)
	authService := auth.NewService(userRepo, redisclient.NewSessionStore(rdb), cfg.JWT.Issuer, cfg.JWT.Secret, cfg.JWT.AccessTTL, cfg.JWT.RefreshTTL, userService, wechatClient)
	userService = userService.WithSessionRevoker(authService)
	materialStore, err := academicinfra.NewEncryptedMaterialStore(
		cfg.Academic.MaterialRoot,
		cfg.Secret.AcademicMaterialKey,
	)
	if err != nil {
		return nil, err
	}
	academicProvider, err := academicinfra.NewMockProvider(cfg.Academic.ProviderFile)
	if err != nil {
		return nil, err
	}
	academicService := academicapp.NewManager(
		academicinfra.NewStore(db, permissionService),
		materialStore,
		academicProvider,
		redisclient.NewAcademicLimiter(rdb, cfg.IsProduction()),
	)
	noticeService := noticeinfra.NewManager(db, nil)
	activityService := activityinfra.NewManager(db, cipher)
	marketplaceService := marketplaceinfra.NewManager(db, cipher)
	errandService := errandinfra.NewManager(db, cipher)
	carpoolService := carpoolinfra.NewManager(db, cipher)
	tradeService := tradeinfra.NewManager(db, nil)
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql db: %w", err)
	}
	h := httpapi.New(authService, userService, permissionService, configService, sqlDB.PingContext, func(c context.Context) error { return rdb.Ping(c).Err() }, log).
		WithDatabase(db).
		WithRequestLimits(cfg.Server.MaxBodyBytes, cfg.Server.MaxHeaderBytes).
		WithProxyPolicy(cfg.Server.TrustedProxyCIDRs, cfg.Server.RequireProxyHTTPS).
		WithAuthLimiter(redisclient.NewAuthLimiter(rdb, cfg.IsProduction())).
		WithNotices(noticeService).
		WithActivities(activityService).
		WithMarketplace(marketplaceService).
		WithErrands(errandService).
		WithCarpools(carpoolService).
		WithTrades(tradeService).
		WithAcademicVerification(academicService)
	router, err := h.Router()
	if err != nil {
		return nil, err
	}
	permissionService.StartSync(ctx, redisclient.NewPolicyNotifier(rdb))
	runtime.Router = router
	runtime.Users = userService
	runtime.Permissions = permissionService
	runtime.Notices = noticeService
	runtime.Activities = activityService
	runtime.Marketplace = marketplaceService
	runtime.Errands = errandService
	runtime.Carpools = carpoolService
	runtime.Trades = tradeService
	runtime.Academic = academicService
	runtime.WeChat = wechatResolver
	initialized = true
	return runtime, nil
}

// Close releases runtime resources.
func (r *Runtime) Close() error {
	var first error
	if r.Permissions != nil {
		r.Permissions.StopSync()
	}
	if r.WeChat != nil {
		r.WeChat.Stop()
	}
	if r.Redis != nil {
		if err := r.Redis.Close(); err != nil {
			first = err
		}
	}
	if r.DB != nil {
		if db, err := r.DB.DB(); err == nil {
			if e := db.Close(); e != nil && first == nil {
				first = e
			}
		}
	}
	if r.Logger != nil {
		if err := r.Logger.Sync(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
