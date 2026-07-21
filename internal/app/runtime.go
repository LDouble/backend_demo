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
	rdb, err := redisclient.Open(ctx, cfg.Redis.Address, cfg.Redis.Password, cfg.Redis.DB)
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
	userService := user.NewService(userRepo, permissionService)
	cipher, err := configcenter.NewCipher(cfg.Secret.ConfigKey)
	if err != nil {
		return nil, err
	}
	configService := configcenter.NewService(mysql.NewConfigRepository(db), cipher)
	authService := auth.NewService(userRepo, redisclient.NewSessionStore(rdb), cfg.JWT.Issuer, cfg.JWT.Secret, cfg.JWT.AccessTTL, cfg.JWT.RefreshTTL)
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql db: %w", err)
	}
	h := httpapi.New(authService, userService, permissionService, configService, sqlDB.PingContext, func(c context.Context) error { return rdb.Ping(c).Err() }, log)
	router, err := h.Router()
	if err != nil {
		return nil, err
	}
	return &Runtime{Config: cfg, DB: db, Redis: rdb, Logger: log, Router: router, Users: userService, Permissions: permissionService}, nil
}

// Close releases runtime resources.
func (r *Runtime) Close() error {
	var first error
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
