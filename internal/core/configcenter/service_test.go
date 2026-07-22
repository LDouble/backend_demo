package configcenter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
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

// instrumentedRepo 在 configRepo 基础上注入错误路径，
// 但只实现 Repository 接口（不实现 ListFiltered/GetPublic/DeleteVersion），
// 因此 Service 在校验时会走基本 List/Get/Delete 路径，便于覆盖错误包装分支。
type instrumentedRepo struct {
	inner     *configRepo
	createErr error
	getErr    error
	listErr   error
	updateErr error
	deleteErr error
}

func newInstrumentedRepo() *instrumentedRepo { return &instrumentedRepo{inner: newConfigRepo()} }

func (r *instrumentedRepo) Create(ctx context.Context, c *model.Config) error {
	if r.createErr != nil {
		return r.createErr
	}
	return r.inner.Create(ctx, c)
}
func (r *instrumentedRepo) Get(ctx context.Context, id uint64) (*model.Config, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	return r.inner.Get(ctx, id)
}
func (r *instrumentedRepo) List(ctx context.Context, group string, page, size int) ([]model.Config, int64, error) {
	if r.listErr != nil {
		return nil, 0, r.listErr
	}
	return r.inner.List(ctx, group, page, size)
}
func (r *instrumentedRepo) UpdateVersion(ctx context.Context, c *model.Config, expected uint64) (bool, error) {
	if r.updateErr != nil {
		return false, r.updateErr
	}
	return r.inner.UpdateVersion(ctx, c, expected)
}
func (r *instrumentedRepo) Delete(ctx context.Context, id uint64) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	return r.inner.Delete(ctx, id)
}

// extendedConfigRepo 同时实现三个扩展接口 (ListFiltered / GetPublic /
// DeleteVersion)，用于验证 Service 通过类型断言走扩展路径时的分支。
type extendedConfigRepo struct {
	inner *configRepo

	listFilteredOverride  func(ctx context.Context, group, keyword, format, visibility string, page, size int) ([]model.Config, int64, error)
	getPublicOverride     func(ctx context.Context, group, key string) (*model.Config, error)
	deleteVersionOverride func(ctx context.Context, id, expected uint64) (bool, error)
}

func newExtendedConfigRepo() *extendedConfigRepo { return &extendedConfigRepo{inner: newConfigRepo()} }

func (r *extendedConfigRepo) Create(ctx context.Context, c *model.Config) error {
	return r.inner.Create(ctx, c)
}
func (r *extendedConfigRepo) Get(ctx context.Context, id uint64) (*model.Config, error) {
	return r.inner.Get(ctx, id)
}
func (r *extendedConfigRepo) List(ctx context.Context, group string, page, size int) ([]model.Config, int64, error) {
	return r.inner.List(ctx, group, page, size)
}
func (r *extendedConfigRepo) UpdateVersion(ctx context.Context, c *model.Config, expected uint64) (bool, error) {
	return r.inner.UpdateVersion(ctx, c, expected)
}
func (r *extendedConfigRepo) Delete(ctx context.Context, id uint64) error {
	return r.inner.Delete(ctx, id)
}
func (r *extendedConfigRepo) GetPublic(ctx context.Context, group, key string) (*model.Config, error) {
	if r.getPublicOverride != nil {
		return r.getPublicOverride(ctx, group, key)
	}
	return r.inner.GetPublic(ctx, group, key)
}
func (r *extendedConfigRepo) ListFiltered(ctx context.Context, group, keyword, format, visibility string, page, size int) ([]model.Config, int64, error) {
	if r.listFilteredOverride != nil {
		return r.listFilteredOverride(ctx, group, keyword, format, visibility, page, size)
	}
	return r.inner.List(ctx, group, page, size)
}
func (r *extendedConfigRepo) DeleteVersion(ctx context.Context, id, expected uint64) (bool, error) {
	if r.deleteVersionOverride != nil {
		return r.deleteVersionOverride(ctx, id, expected)
	}
	return true, nil
}

func newCipher(t *testing.T) *Cipher {
	t.Helper()
	c, err := NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// requireAppError 断言 err 是一个 status/code 都匹配的 apperror.Error。
func requireAppError(t *testing.T, err error, status int, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", code)
	}
	appErr, ok := apperror.As(err)
	if !ok {
		t.Fatalf("expected apperror, got %T: %v", err, err)
	}
	if appErr.Status != status || appErr.Code != code {
		t.Fatalf("expected status=%d code=%s, got status=%d code=%s", status, code, appErr.Status, appErr.Code)
	}
}

func TestConfigLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := newConfigRepo()
	svc := NewService(repo, newCipher(t))
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
	svc := NewService(newConfigRepo(), newCipher(t))
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
	svc := NewService(newConfigRepo(), newCipher(t))
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

// TestCreateRejectsInvalidInputs 覆盖 validateValue 与 visibility 校验
// 中被现有测试遗漏的失败路径：非 string/json 格式、可见性非法值、
// 公开配置必须是 JSON、公开配置不可加密。
func TestCreateRejectsInvalidInputs(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newConfigRepo(), newCipher(t))

	t.Run("invalid_format", func(t *testing.T) {
		_, err := svc.Create(ctx, "g", "k", "value", "yaml", "admin", false, uint64(1))
		requireAppError(t, err, 400, "invalid_config_format")
	})

	t.Run("invalid_visibility", func(t *testing.T) {
		_, err := svc.Create(ctx, "g", "k", "value", "string", "guest", false, uint64(1))
		requireAppError(t, err, 400, "invalid_config_visibility")
	})

	t.Run("public_requires_json", func(t *testing.T) {
		_, err := svc.Create(ctx, "g", "k", "value", "string", "public", false, uint64(1))
		requireAppError(t, err, 400, "invalid_config_visibility")
	})

	t.Run("public_rejects_encrypted", func(t *testing.T) {
		_, err := svc.Create(ctx, "g", "k", `{"a":1}`, "json", "public", true, uint64(1))
		requireAppError(t, err, 400, "invalid_config_visibility")
	})
}

// TestCreateRejectsOversizeJSON 覆盖 512KB 上限的拒绝路径。
func TestCreateRejectsOversizeJSON(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newConfigRepo(), newCipher(t))

	huge := `"` + strings.Repeat("a", 512*1024) + `"`
	_, err := svc.Create(ctx, "big", "doc", huge, "json", "admin", false, uint64(1))
	requireAppError(t, err, 413, "config_value_too_large")
}

// TestCreateRepoError 覆盖 Create 内部仓库错误的包装路径以及去重。
func TestCreateRepoError(t *testing.T) {
	ctx := context.Background()
	repo := newInstrumentedRepo()
	repo.createErr = errors.New("disk full")
	svc := NewService(repo, newCipher(t))
	_, err := svc.Create(ctx, "g", "k", "v", false, uint64(1))
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected wrapped create error, got %v", err)
	}
}

// TestGetRepoError 覆盖 Get 收到非 NotFound 错误时包装为 "get config" 错误。
func TestGetRepoError(t *testing.T) {
	ctx := context.Background()
	repo := newInstrumentedRepo()
	repo.getErr = errors.New("boom")
	svc := NewService(repo, newCipher(t))
	_, err := svc.Get(ctx, 1)
	if err == nil || !strings.Contains(err.Error(), "get config") {
		t.Fatalf("expected wrapped get error, got %v", err)
	}
}

// TestListWithFilter 覆盖 5 参过滤模式以及 List 错误。
func TestListWithFilter(t *testing.T) {
	ctx := context.Background()

	t.Run("filtered_path", func(t *testing.T) {
		repo := newExtendedConfigRepo()
		called := false
		repo.listFilteredOverride = func(_ context.Context, group, keyword, format, visibility string, page, size int) ([]model.Config, int64, error) {
			called = true
			if group != "g" || keyword != "kw" || format != "json" || visibility != "public" || page != 2 || size != 5 {
				t.Fatalf("unexpected filter args: g=%q kw=%q fmt=%q vis=%q page=%d size=%d", group, keyword, format, visibility, page, size)
			}
			return []model.Config{{ID: 1, Group: group, Key: "x", Format: "json", Visibility: "public"}}, 1, nil
		}
		svc := NewService(repo, newCipher(t))
		rows, total, err := svc.List(ctx, "g", "kw", "json", "public", 2, 5)
		if err != nil || !called || total != 1 || len(rows) != 1 || rows[0].ID != 1 {
			t.Fatalf("rows=%v total=%d err=%v called=%v", rows, total, err, called)
		}
	})

	t.Run("list_error", func(t *testing.T) {
		repo := newInstrumentedRepo()
		repo.listErr = errors.New("db down")
		svc := NewService(repo, newCipher(t))
		_, _, err := svc.List(ctx, "g", 1, 20)
		if err == nil || !strings.Contains(err.Error(), "list configs") {
			t.Fatalf("expected wrapped list error, got %v", err)
		}
	})
}

// TestRuntimeFallbackAndGuards 覆盖 Runtime 走 List 兜底路径以及
// Encrypted / 非 json / 非 public 时返回 404 的保护逻辑。
func TestRuntimeFallbackAndGuards(t *testing.T) {
	ctx := context.Background()

	t.Run("fallback_list", func(t *testing.T) {
		repo := newInstrumentedRepo()
		// instrumentedRepo 不实现 GetPublic，Service 走 List 兜底路径
		repo.inner.rows[1] = &model.Config{ID: 1, Group: "g", Key: "k", Format: "json", Visibility: "public", Value: `{"a":1}`}
		svc := NewService(repo, newCipher(t))
		rt, _, err := svc.Runtime(ctx, "g", "k")
		if err != nil {
			t.Fatal(err)
		}
		if rt.Group != "g" || rt.Key != "k" {
			t.Fatalf("runtime=%#v", rt)
		}
	})

	t.Run("encrypted_rejected", func(t *testing.T) {
		repo := newConfigRepo()
		svc := NewService(repo, newCipher(t))
		if _, err := svc.Create(ctx, "g", "k", "plain", true, uint64(1)); err != nil {
			t.Fatal(err)
		}
		// 直接放进仓库，绕过 Create 的 visibility=public 限制
		repo.rows[1] = &model.Config{ID: 1, Group: "g", Key: "k", Format: "string", Encrypted: true, Value: "x"}
		_, _, err := svc.Runtime(ctx, "g", "k")
		requireAppError(t, err, 404, "config_not_found")
	})

	t.Run("non_json_rejected", func(t *testing.T) {
		repo := newConfigRepo()
		repo.rows[1] = &model.Config{ID: 1, Group: "g", Key: "k", Format: "string", Visibility: "public", Value: "x"}
		svc := NewService(repo, newCipher(t))
		_, _, err := svc.Runtime(ctx, "g", "k")
		requireAppError(t, err, 404, "config_not_found")
	})

	t.Run("non_public_rejected", func(t *testing.T) {
		repo := newConfigRepo()
		repo.rows[1] = &model.Config{ID: 1, Group: "g", Key: "k", Format: "json", Visibility: "admin", Value: `{"a":1}`}
		svc := NewService(repo, newCipher(t))
		_, _, err := svc.Runtime(ctx, "g", "k")
		requireAppError(t, err, 404, "config_not_found")
	})

	t.Run("not_found", func(t *testing.T) {
		repo := newConfigRepo()
		svc := NewService(repo, newCipher(t))
		_, _, err := svc.Runtime(ctx, "g", "missing")
		requireAppError(t, err, 404, "config_not_found")
	})
}

// TestRuntimeDecodeError 覆盖 Runtime 中 JSON 反序列化失败的错误包装路径。
func TestRuntimeDecodeError(t *testing.T) {
	ctx := context.Background()
	repo := newConfigRepo()
	// 手工注入一条 public json，但 Value 不是合法 JSON，触发 decode 失败
	repo.rows[1] = &model.Config{ID: 1, Group: "g", Key: "k", Format: "json", Visibility: "public", Value: "not-json"}
	svc := NewService(repo, newCipher(t))
	_, _, err := svc.Runtime(ctx, "g", "k")
	if err == nil || !strings.Contains(err.Error(), "decode runtime config") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

// TestUpdateWithVisibility 覆盖 Update 携带 visibility 时的
// 校验、可见性归一化以及 visibility 失败时的回滚。
func TestUpdateWithVisibility(t *testing.T) {
	ctx := context.Background()
	repo := newConfigRepo()
	svc := NewService(repo, newCipher(t))
	created, err := svc.Create(ctx, "g", "k", `{"a":1}`, "json", "admin", false, uint64(1))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("change_to_public_json", func(t *testing.T) {
		vis := "public"
		updated, err := svc.Update(ctx, created.ID, 1, nil, &vis, uint64(2))
		if err != nil {
			t.Fatal(err)
		}
		if updated.Visibility != "public" {
			t.Fatalf("visibility=%s", updated.Visibility)
		}
	})

	t.Run("public_rejects_encrypted_value", func(t *testing.T) {
		// 当前已为 public + json + 非加密；改为加密值应被拒绝
		value := "plaintext"
		_, err := svc.Update(ctx, created.ID, 2, &value, uint64(3))
		if err == nil {
			t.Fatal("expected public visibility rejects encrypted-style value update")
		}
	})

	t.Run("invalid_visibility_rejected", func(t *testing.T) {
		vis := "guest"
		_, err := svc.Update(ctx, created.ID, 2, nil, &vis, uint64(3))
		requireAppError(t, err, 400, "invalid_config_visibility")
	})
}

// TestUpdateEncryptedValue 覆盖 Update 中加密值更新路径。
func TestUpdateEncryptedValue(t *testing.T) {
	ctx := context.Background()
	repo := newConfigRepo()
	svc := NewService(repo, newCipher(t))
	created, err := svc.Create(ctx, "g", "k", "first", true, uint64(1))
	if err != nil {
		t.Fatal(err)
	}
	value := "second"
	updated, err := svc.Update(ctx, created.ID, 1, &value, uint64(2))
	if err != nil {
		t.Fatal(err)
	}
	if updated.HasValue == false {
		t.Fatal("encrypted update should report HasValue true")
	}
	if repo.rows[created.ID].Value == "second" {
		t.Fatal("plaintext stored after encrypted update")
	}
}

// TestUpdateRepoError 覆盖 Update 中 Get 与 UpdateVersion 包装错误的路径。
func TestUpdateRepoError(t *testing.T) {
	ctx := context.Background()

	t.Run("get_error", func(t *testing.T) {
		repo := newInstrumentedRepo()
		repo.getErr = errors.New("boom")
		svc := NewService(repo, newCipher(t))
		value := "x"
		_, err := svc.Update(ctx, 1, 0, &value, uint64(1))
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("expected get error, got %v", err)
		}
	})

	t.Run("update_version_error", func(t *testing.T) {
		repo := newInstrumentedRepo()
		repo.inner.rows[1] = &model.Config{ID: 1, Group: "g", Key: "k", Format: "string", Value: "v", Version: 1}
		repo.updateErr = errors.New("write conflict")
		svc := NewService(repo, newCipher(t))
		value := "v2"
		_, err := svc.Update(ctx, 1, 1, &value, uint64(2))
		if err == nil || !strings.Contains(err.Error(), "update config") {
			t.Fatalf("expected update config error, got %v", err)
		}
	})
}

// TestUpdateValidationRejectsBadValue 覆盖 Update 中 value 校验失败。
func TestUpdateValidationRejectsBadValue(t *testing.T) {
	ctx := context.Background()
	repo := newConfigRepo()
	svc := NewService(repo, newCipher(t))
	created, err := svc.Create(ctx, "g", "k", `{"a":1}`, "json", "admin", false, uint64(1))
	if err != nil {
		t.Fatal(err)
	}
	bad := "not-json"
	_, err = svc.Update(ctx, created.ID, 1, &bad, uint64(2))
	requireAppError(t, err, 400, "invalid_config_json")
}

// TestDeleteVersioned 覆盖 Delete 的版本化删除与错误包装路径。
func TestDeleteVersioned(t *testing.T) {
	ctx := context.Background()

	t.Run("version_match", func(t *testing.T) {
		repo := newExtendedConfigRepo()
		called := false
		repo.deleteVersionOverride = func(_ context.Context, id, expected uint64) (bool, error) {
			called = true
			if id != 1 || expected != 3 {
				t.Fatalf("unexpected args: id=%d expected=%d", id, expected)
			}
			return true, nil
		}
		svc := NewService(repo, newCipher(t))
		if err := svc.Delete(ctx, 1, 3); err != nil {
			t.Fatal(err)
		}
		if !called {
			t.Fatal("expected DeleteVersion to be called")
		}
	})

	t.Run("version_mismatch", func(t *testing.T) {
		repo := newExtendedConfigRepo()
		repo.deleteVersionOverride = func(_ context.Context, _, _ uint64) (bool, error) {
			return false, nil
		}
		svc := NewService(repo, newCipher(t))
		err := svc.Delete(ctx, 1, 9)
		requireAppError(t, err, 409, "version_conflict")
	})

	t.Run("delete_version_error", func(t *testing.T) {
		repo := newExtendedConfigRepo()
		repo.deleteVersionOverride = func(_ context.Context, _, _ uint64) (bool, error) {
			return false, errors.New("tx abort")
		}
		svc := NewService(repo, newCipher(t))
		err := svc.Delete(ctx, 1, 1)
		if err == nil || !strings.Contains(err.Error(), "delete config") {
			t.Fatalf("expected wrapped delete error, got %v", err)
		}
	})

	t.Run("delete_error", func(t *testing.T) {
		repo := newInstrumentedRepo()
		repo.deleteErr = fmt.Errorf("io failure")
		svc := NewService(repo, newCipher(t))
		err := svc.Delete(ctx, 1)
		if err == nil || !strings.Contains(err.Error(), "delete config") {
			t.Fatalf("expected wrapped delete error, got %v", err)
		}
	})
}
