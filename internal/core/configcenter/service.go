package configcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

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

type filteredRepository interface {
	ListFiltered(context.Context, string, string, string, string, int, int) ([]model.Config, int64, error)
}
type runtimeRepository interface {
	GetPublic(context.Context, string, string) (*model.Config, error)
}
type versionedDeleteRepository interface {
	DeleteVersion(context.Context, uint64, uint64) (bool, error)
}

// RuntimeView is the public, non-secret representation of a JSON document.
type RuntimeView struct {
	Group     string    `json:"group"`
	Key       string    `json:"key"`
	Value     any       `json:"value"`
	Version   uint64    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// View is the safe API representation of a configuration.
type View struct {
	ID         uint64    `json:"id"`
	Group      string    `json:"group"`
	Key        string    `json:"key"`
	Value      *string   `json:"value"`
	Encrypted  bool      `json:"encrypted"`
	Format     string    `json:"format"`
	Visibility string    `json:"visibility"`
	HasValue   bool      `json:"has_value"`
	Version    uint64    `json:"version"`
	UpdatedBy  uint64    `json:"updated_by"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
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
	return View{ID: c.ID, Group: c.Group, Key: c.Key, Value: value, Encrypted: c.Encrypted, Format: normalizedFormat(c.Format), Visibility: normalizedVisibility(c.Visibility), HasValue: c.Value != "", Version: c.Version, UpdatedBy: c.UpdatedBy, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

func validateValue(format, value string) error {
	format = normalizedFormat(format)
	if format != "string" && format != "json" {
		return apperror.New(400, "invalid_config_format", "配置格式无效")
	}
	if format == "json" {
		if len(value) > 512*1024 {
			return apperror.New(413, "config_value_too_large", "配置 JSON 过大")
		}
		var parsed any
		if err := json.Unmarshal([]byte(value), &parsed); err != nil || parsed == nil {
			return apperror.New(400, "invalid_config_json", "配置值必须是合法 JSON")
		}
	}
	return nil
}

func normalizedFormat(value string) string {
	if value == "" {
		return "string"
	}
	return value
}
func normalizedVisibility(value string) string {
	if value == "" {
		return "admin"
	}
	return value
}
func canonicalValue(format, value string) string {
	if normalizedFormat(format) != "json" {
		return value
	}
	var parsed any
	if json.Unmarshal([]byte(value), &parsed) != nil {
		return value
	}
	data, err := json.Marshal(parsed)
	if err != nil {
		return value
	}
	return string(data)
}

// Create adds a runtime configuration value.
func (s *Service) Create(ctx context.Context, group, key, value string, args ...any) (View, error) {
	format, visibility, encrypted, actor := "string", "admin", false, uint64(0)
	if len(args) == 2 {
		encrypted, _ = args[0].(bool)
		actor, _ = args[1].(uint64)
	}
	if len(args) == 4 {
		format, _ = args[0].(string)
		visibility, _ = args[1].(string)
		encrypted, _ = args[2].(bool)
		actor, _ = args[3].(uint64)
	}
	if err := validate(group, key); err != nil {
		return View{}, err
	}
	format = normalizedFormat(format)
	visibility = normalizedVisibility(visibility)
	if err := validateValue(format, value); err != nil {
		return View{}, err
	}
	if visibility != "admin" && visibility != "public" {
		return View{}, apperror.New(400, "invalid_config_visibility", "配置可见性无效")
	}
	if visibility == "public" && (format != "json" || encrypted) {
		return View{}, apperror.New(400, "invalid_config_visibility", "公开配置必须是非加密 JSON")
	}
	c := &model.Config{Group: strings.TrimSpace(group), Key: strings.TrimSpace(key), Format: format, Visibility: visibility, Encrypted: encrypted, Version: 1, UpdatedBy: actor}
	if encrypted {
		enc, err := s.cipher.Encrypt(value, c.Group+"."+c.Key)
		if err != nil {
			return View{}, err
		}
		c.Value = enc
	} else {
		c.Value = canonicalValue(format, value)
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
func (s *Service) List(ctx context.Context, group string, args ...any) ([]View, int64, error) {
	page, size, keyword, format, visibility := 1, 20, "", "", ""
	if len(args) == 2 {
		page, _ = args[0].(int)
		size, _ = args[1].(int)
	}
	if len(args) == 5 {
		keyword, _ = args[0].(string)
		format, _ = args[1].(string)
		visibility, _ = args[2].(string)
		page, _ = args[3].(int)
		size, _ = args[4].(int)
	}
	var rows []model.Config
	var total int64
	var err error
	if filtered, ok := s.repo.(filteredRepository); ok {
		rows, total, err = filtered.ListFiltered(ctx, group, keyword, format, visibility, page, size)
	} else {
		rows, total, err = s.repo.List(ctx, group, page, size)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("list configs: %w", err)
	}
	out := make([]View, 0, len(rows))
	for i := range rows {
		out = append(out, view(&rows[i]))
	}
	return out, total, nil
}

// Runtime returns a public JSON document when its visibility permits exposure.
func (s *Service) Runtime(ctx context.Context, group, key string) (RuntimeView, uint64, error) {
	var config *model.Config
	var err error
	if runtime, ok := s.repo.(runtimeRepository); ok {
		config, err = runtime.GetPublic(ctx, group, key)
	} else {
		var rows []model.Config
		rows, _, err = s.repo.List(ctx, group, 1, 100)
		for i := range rows {
			if rows[i].Key == key {
				config = &rows[i]
				break
			}
		}
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return RuntimeView{}, 0, apperror.New(404, "config_not_found", "配置不存在")
		}
		return RuntimeView{}, 0, fmt.Errorf("get runtime config: %w", err)
	}
	if config == nil {
		return RuntimeView{}, 0, apperror.New(404, "config_not_found", "配置不存在")
	}
	if config.Encrypted || normalizedFormat(config.Format) != "json" || normalizedVisibility(config.Visibility) != "public" {
		return RuntimeView{}, 0, apperror.New(404, "config_not_found", "配置不存在")
	}
	var value any
	if err := json.Unmarshal([]byte(config.Value), &value); err != nil {
		return RuntimeView{}, 0, fmt.Errorf("decode runtime config: %w", err)
	}
	return RuntimeView{Group: config.Group, Key: config.Key, Value: value, Version: config.Version, UpdatedAt: config.UpdatedAt}, config.ID, nil
}

// Update changes a value when its expected version still matches.
func (s *Service) Update(ctx context.Context, id, expected uint64, value *string, args ...any) (View, error) {
	visibility, actor := (*string)(nil), uint64(0)
	if len(args) == 1 {
		actor, _ = args[0].(uint64)
	}
	if len(args) == 2 {
		visibility, _ = args[0].(*string)
		actor, _ = args[1].(uint64)
	}
	c, err := s.repo.Get(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return View{}, apperror.New(404, "config_not_found", "配置不存在")
	}
	if err != nil {
		return View{}, err
	}
	if value != nil {
		if err := validateValue(c.Format, *value); err != nil {
			return View{}, err
		}
		if c.Encrypted {
			enc, e := s.cipher.Encrypt(*value, c.Group+"."+c.Key)
			if e != nil {
				return View{}, e
			}
			c.Value = enc
		} else {
			c.Value = canonicalValue(c.Format, *value)
		}
	}
	if visibility != nil {
		v := normalizedVisibility(*visibility)
		if v != "admin" && v != "public" {
			return View{}, apperror.New(400, "invalid_config_visibility", "配置可见性无效")
		}
		if v == "public" && (normalizedFormat(c.Format) != "json" || c.Encrypted) {
			return View{}, apperror.New(400, "invalid_config_visibility", "公开配置必须是非加密 JSON")
		}
		c.Visibility = v
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
func (s *Service) Delete(ctx context.Context, id uint64, expected ...uint64) error {
	if versioned, ok := s.repo.(versionedDeleteRepository); ok && len(expected) > 0 {
		matched, err := versioned.DeleteVersion(ctx, id, expected[0])
		if err != nil {
			return fmt.Errorf("delete config: %w", err)
		}
		if !matched {
			return apperror.New(409, "version_conflict", "配置已被其他请求更新")
		}
		return nil
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete config: %w", err)
	}
	return nil
}
