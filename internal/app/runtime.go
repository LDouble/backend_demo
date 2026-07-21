// Package app wires the platform components.
package app

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/weouc-plus/campus-platform/internal/api/httpapi"
	"github.com/weouc-plus/campus-platform/internal/core/auth"
	"github.com/weouc-plus/campus-platform/internal/core/bootstrap"
	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/core/permission"
	"github.com/weouc-plus/campus-platform/internal/core/user"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/logger"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/mysql"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/redisclient"
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
}

// Build initializes all runtime dependencies.
func Build(ctx context.Context, cfg bootstrap.Config) (*Runtime, error) {
	log, err := logger.New()
	if err != nil {
		return nil, fmt.Errorf("create logger: %w", err)
	}
	db, err := mysql.Open(ctx, cfg.MySQL.DSN)
	if err != nil {
		_ = log.Sync()
		return nil, err
	}
	rdb, err := redisclient.Open(ctx, cfg.Redis, cfg.Redis.DB)
	if err != nil {
		_ = log.Sync()
		return nil, err
	}
	userRepo := mysql.NewUserRepository(db)
	roleRepo := mysql.NewRoleRepository(db)
	permissionService, err := permission.NewService(db, roleRepo)
	if err != nil {
		return nil, err
	}
	permissionService.WithLogger(log)
	cipher, err := configcenter.NewCipher(cfg.Secret.ConfigKey)
	if err != nil {
		return nil, err
	}
	configService := configcenter.NewService(mysql.NewConfigRepository(db), cipher)
	authService := auth.NewService(userRepo, redisclient.NewSessionStore(rdb), cfg.JWT.Issuer, cfg.JWT.Secret, cfg.JWT.AccessTTL, cfg.JWT.RefreshTTL)
	userService := user.NewService(userRepo, permissionService).WithSessionRevoker(authService)
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
		WithAuthLimiter(redisclient.NewAuthLimiter(rdb, cfg.Environment == "production")).
		WithNotices(noticeService).
		WithActivities(activityService).
		WithMarketplace(marketplaceService).
		WithErrands(errandService).
		WithCarpools(carpoolService).
		WithTrades(tradeService)
	router, err := h.Router()
	if err != nil {
		return nil, err
	}
	permissionService.StartSync(ctx, redisclient.NewPolicyNotifier(rdb))
	return &Runtime{Config: cfg, DB: db, Redis: rdb, Logger: log, Router: router, Users: userService, Permissions: permissionService, Notices: noticeService, Activities: activityService, Marketplace: marketplaceService, Errands: errandService, Carpools: carpoolService, Trades: tradeService}, nil
}

// Close releases runtime resources.
func (r *Runtime) Close() error {
	var first error
	if r.Permissions != nil {
		r.Permissions.StopSync()
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
