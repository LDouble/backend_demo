package configcenter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"gorm.io/gorm"
)

// Repository persists runtime configuration.
type Repository interface {
	Create(context.Context, *model.Config) error
	Get(context.Context, uint64) (*model.Config, error)
	List(context.Context, string, int, int) ([]model.Config, int64, error)
	UpdateVersion(context.Context, *model.Config, uint64) (bool, error)
	Delete(context.Context, uint64) error
}

// View is the safe API representation of a configuration.
type View struct {
	ID        uint64  `json:"id"`
	Group     string  `json:"group"`
	Key       string  `json:"key"`
	Value     *string `json:"value"`
	Encrypted bool    `json:"encrypted"`
	HasValue  bool    `json:"has_value"`
	Version   uint64  `json:"version"`
	UpdatedBy uint64  `json:"updated_by"`
}

// Service manages runtime configurations.
type Service struct {
	repo   Repository
	cipher *Cipher
}

// NewService creates a runtime configuration service.
func NewService(repo Repository, cipher *Cipher) *Service {
	return &Service{repo: repo, cipher: cipher}
}

func validate(group, key string) error {
	if strings.TrimSpace(group) == "" || strings.TrimSpace(key) == "" {
		return apperror.New(400, "invalid_config_key", "配置分组和键不能为空")
	}
	return nil
}
func view(c *model.Config) View {
	var value *string
	if !c.Encrypted {
		v := c.Value
		value = &v
	}
	return View{ID: c.ID, Group: c.Group, Key: c.Key, Value: value, Encrypted: c.Encrypted, HasValue: c.Value != "", Version: c.Version, UpdatedBy: c.UpdatedBy}
}

// Create adds a runtime configuration value.
func (s *Service) Create(ctx context.Context, group, key, value string, encrypted bool, actor uint64) (View, error) {
	if err := validate(group, key); err != nil {
		return View{}, err
	}
	c := &model.Config{Group: strings.TrimSpace(group), Key: strings.TrimSpace(key), Encrypted: encrypted, Version: 1, UpdatedBy: actor}
	if encrypted {
		enc, err := s.cipher.Encrypt(value, c.Group+"."+c.Key)
		if err != nil {
			return View{}, err
		}
		c.Value = enc
	} else {
		c.Value = value
	}
	if err := s.repo.Create(ctx, c); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return View{}, apperror.New(http.StatusConflict, "config_exists", "配置已存在")
		}
		return View{}, fmt.Errorf("create config: %w", err)
	}
	return view(c), nil
}

// Get returns one safely redacted configuration value.
func (s *Service) Get(ctx context.Context, id uint64) (View, error) {
	c, err := s.repo.Get(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return View{}, apperror.New(404, "config_not_found", "配置不存在")
	}
	if err != nil {
		return View{}, fmt.Errorf("get config: %w", err)
	}
	return view(c), nil
}

// List returns a page of safely redacted configuration values.
func (s *Service) List(ctx context.Context, group string, page, size int) ([]View, int64, error) {
	rows, total, err := s.repo.List(ctx, group, page, size)
	if err != nil {
		return nil, 0, fmt.Errorf("list configs: %w", err)
	}
	out := make([]View, 0, len(rows))
	for i := range rows {
		out = append(out, view(&rows[i]))
	}
	return out, total, nil
}

// Update changes a value when its expected version still matches.
func (s *Service) Update(ctx context.Context, id, expected uint64, value *string, actor uint64) (View, error) {
	c, err := s.repo.Get(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return View{}, apperror.New(404, "config_not_found", "配置不存在")
	}
	if err != nil {
		return View{}, err
	}
	if value != nil {
		if c.Encrypted {
			enc, e := s.cipher.Encrypt(*value, c.Group+"."+c.Key)
			if e != nil {
				return View{}, e
			}
			c.Value = enc
		} else {
			c.Value = *value
		}
	}
	c.UpdatedBy = actor
	c.Version = expected + 1
	ok, err := s.repo.UpdateVersion(ctx, c, expected)
	if err != nil {
		return View{}, fmt.Errorf("update config: %w", err)
	}
	if !ok {
		return View{}, apperror.New(409, "version_conflict", "配置已被其他请求更新")
	}
	return view(c), nil
}

// Delete removes a runtime configuration value.
func (s *Service) Delete(ctx context.Context, id uint64) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete config: %w", err)
	}
	return nil
}
