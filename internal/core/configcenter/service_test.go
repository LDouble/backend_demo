package configcenter

import (
	"context"
	"testing"

	"github.com/weouc-plus/campus-platform/internal/core/model"
	"gorm.io/gorm"
)

type configRepo struct {
	rows map[uint64]*model.Config
	next uint64
}

func newConfigRepo() *configRepo { return &configRepo{rows: map[uint64]*model.Config{}, next: 1} }
func (r *configRepo) Create(_ context.Context, c *model.Config) error {
	for _, v := range r.rows {
		if v.Group == c.Group && v.Key == c.Key {
			return gorm.ErrDuplicatedKey
		}
	}
	c.ID = r.next
	r.next++
	clone := *c
	r.rows[c.ID] = &clone
	return nil
}
func (r *configRepo) Get(_ context.Context, id uint64) (*model.Config, error) {
	v, ok := r.rows[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *v
	return &clone, nil
}
func (r *configRepo) List(_ context.Context, group string, _, _ int) ([]model.Config, int64, error) {
	out := []model.Config{}
	for _, v := range r.rows {
		if group == "" || v.Group == group {
			out = append(out, *v)
		}
	}
	return out, int64(len(out)), nil
}
func (r *configRepo) GetPublic(_ context.Context, group, key string) (*model.Config, error) {
	for _, v := range r.rows {
		if v.Group == group && v.Key == key && v.Format == "json" && v.Visibility == "public" && !v.Encrypted {
			clone := *v
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (r *configRepo) UpdateVersion(_ context.Context, c *model.Config, expected uint64) (bool, error) {
	v, ok := r.rows[c.ID]
	if !ok || v.Version != expected {
		return false, nil
	}
	clone := *c
	r.rows[c.ID] = &clone
	return true, nil
}
func (r *configRepo) Delete(_ context.Context, id uint64) error { delete(r.rows, id); return nil }

func TestConfigLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := newConfigRepo()
	cipher, err := NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(repo, cipher)
	created, err := svc.Create(ctx, "wechat", "secret", "plaintext", true, 1)
	if err != nil {
		t.Fatal(err)
	}
	if created.Value != nil || !created.HasValue {
		t.Fatalf("secret leaked: %#v", created)
	}
	if repo.rows[created.ID].Value == "plaintext" {
		t.Fatal("plaintext stored")
	}
	if _, err = svc.Create(ctx, "wechat", "secret", "duplicate", false, 1); err == nil {
		t.Fatal("duplicate must fail")
	}
	got, err := svc.Get(ctx, created.ID)
	if err != nil || got.Version != 1 {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	value := "new secret"
	updated, err := svc.Update(ctx, created.ID, 1, &value, 2)
	if err != nil || updated.Version != 2 {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	if _, err = svc.Update(ctx, created.ID, 1, &value, 2); err == nil {
		t.Fatal("stale version must fail")
	}
	rows, total, err := svc.List(ctx, "wechat", 1, 20)
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("rows=%v total=%d err=%v", rows, total, err)
	}
	if err = svc.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Get(ctx, created.ID); err == nil {
		t.Fatal("deleted config must not exist")
	}
}
func TestPlainConfigAndValidation(t *testing.T) {
	ctx := context.Background()
	cipher, _ := NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	svc := NewService(newConfigRepo(), cipher)
	v, err := svc.Create(ctx, "feature", "enabled", "true", false, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v.Value == nil || *v.Value != "true" {
		t.Fatalf("value=%v", v.Value)
	}
	if _, err = svc.Create(ctx, "", "key", "x", false, 1); err == nil {
		t.Fatal("empty group must fail")
	}
}

func TestJSONRuntimeConfig(t *testing.T) {
	ctx := context.Background()
	cipher, _ := NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	svc := NewService(newConfigRepo(), cipher)
	created, err := svc.Create(ctx, "miniapp", "startup", `{"home":{"banners":[{"image":"a"}]}}`, "json", "public", false, uint64(7))
	if err != nil {
		t.Fatal(err)
	}
	if created.Format != "json" || created.Visibility != "public" {
		t.Fatalf("metadata=%#v", created)
	}
	runtime, _, err := svc.Runtime(ctx, "miniapp", "startup")
	if err != nil {
		t.Fatal(err)
	}
	home := runtime.Value.(map[string]any)
	if len(home["home"].(map[string]any)["banners"].([]any)) != 1 {
		t.Fatalf("runtime=%#v", runtime.Value)
	}
	if _, err = svc.Create(ctx, "miniapp", "detail", `{"version":1}`, "json", "public", false, uint64(7)); err != nil {
		t.Fatal(err)
	}
	if runtime, _, err = svc.Runtime(ctx, "miniapp", "startup"); err != nil || runtime.Key != "startup" {
		t.Fatalf("runtime=%#v err=%v", runtime, err)
	}
	if _, err = svc.Create(ctx, "miniapp", "bad", "not-json", "json", "public", false, uint64(7)); err == nil {
		t.Fatal("invalid json must fail")
	}
}
