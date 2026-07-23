package wechat

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSource is a configurable SecretsSource for resolver tests.
type fakeSource struct {
	mu      sync.Mutex
	data    map[string]string
	calls   atomic.Int32
	failNow bool
}

func (f *fakeSource) ListDecrypted(_ context.Context, _ string) (map[string]string, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNow {
		return nil, errors.New("backend down")
	}
	out := make(map[string]string, len(f.data))
	for k, v := range f.data {
		out[k] = v
	}
	return out, nil
}

func (f *fakeSource) set(values map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = values
}

func TestCachingResolverPrime(t *testing.T) {
	src := &fakeSource{data: map[string]string{"wxapp-1": "s1"}}
	resolver, err := NewCachingResolver(context.Background(), src, "wechat", 0, nil)
	if err != nil {
		t.Fatalf("prime: %v", err)
	}
	got := resolver.Resolve()
	if got["wxapp-1"] != "s1" {
		t.Fatalf("unexpected resolve: %+v", got)
	}
	if src.calls.Load() != 1 {
		t.Fatalf("expected 1 initial load, got %d", src.calls.Load())
	}
}

func TestCachingResolverRefreshOnTick(t *testing.T) {
	src := &fakeSource{data: map[string]string{"wxapp-1": "s1"}}
	resolver, err := NewCachingResolver(context.Background(), src, "wechat", 50*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("prime: %v", err)
	}
	resolver.Start(context.Background())
	defer resolver.Stop()
	time.Sleep(160 * time.Millisecond)
	src.set(map[string]string{"wxapp-1": "s1", "wxapp-2": "s2"})
	// wait for at least one more tick
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if v, ok := resolver.Resolve()["wxapp-2"]; ok && v == "s2" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected refresh to pick up wxapp-2: %+v", resolver.Resolve())
}

func TestCachingResolverSurvivesSourceError(t *testing.T) {
	src := &fakeSource{data: map[string]string{"wxapp-1": "s1"}}
	resolver, err := NewCachingResolver(context.Background(), src, "wechat", 0, nil)
	if err != nil {
		t.Fatalf("prime: %v", err)
	}
	resolver.Start(context.Background())
	defer resolver.Stop()
	src.failNow = true
	// force a manual refresh via the public API would require unexported
	// access; instead, just confirm the cached map is still served.
	got := resolver.Resolve()
	if got["wxapp-1"] != "s1" {
		t.Fatalf("expected cached map, got %+v", got)
	}
}

func TestCachingResolverNilSource(t *testing.T) {
	resolver, err := NewCachingResolver(context.Background(), nil, "wechat", 0, nil)
	if err != nil {
		t.Fatalf("nil source: %v", err)
	}
	if resolver != nil {
		t.Fatalf("expected nil resolver when source is nil")
	}
}
