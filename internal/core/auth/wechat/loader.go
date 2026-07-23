package wechat

import (
	"context"
	"sync"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"go.uber.org/zap"
)

// SecretsSource returns the current decrypted (appid -> secret) map for a
// configuration group. The configcenter service satisfies this interface
// directly via ListDecrypted.
type SecretsSource interface {
	ListDecrypted(ctx context.Context, group string) (map[string]string, error)
}

// CachingResolver is a thread-safe SecretsResolver backed by a SecretsSource.
//
// On construction it loads the secrets once synchronously so a misconfigured
// deployment fails fast. Call Start to enable background refresh on a fixed
// interval; the loader never blocks a request on a refresh, falling back to
// the previously cached map if the source is unavailable.
type CachingResolver struct {
	source   SecretsSource
	group    string
	interval time.Duration
	logger   *zap.Logger

	mu        sync.RWMutex
	secrets   map[string]string
	stopCh    chan struct{}
	stoppedCh chan struct{}
}

// NewCachingResolver builds and primes a CachingResolver.
//
// group is the configuration center group that holds WeChat appid entries
// (each row's key is the appid and its (decrypted) value is the secret).
// interval is the background refresh cadence; pass 0 to disable refresh.
func NewCachingResolver(ctx context.Context, source SecretsSource, group string, interval time.Duration, logger *zap.Logger) (*CachingResolver, error) {
	if source == nil {
		return nil, nil
	}
	resolver := &CachingResolver{
		source:    source,
		group:     group,
		interval:  interval,
		logger:    logger,
		stopCh:    make(chan struct{}),
		stoppedCh: make(chan struct{}),
	}
	if err := resolver.refresh(ctx); err != nil {
		return nil, err
	}
	return resolver, nil
}

// Resolve returns the cached appid -> secret map. It never blocks on I/O.
func (r *CachingResolver) Resolve() map[string]string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.secrets) == 0 {
		return nil
	}
	out := make(map[string]string, len(r.secrets))
	for k, v := range r.secrets {
		out[k] = v
	}
	return out
}

// Start begins background refresh; returns immediately. Call Stop to halt.
//
// When interval is 0, Start is a no-op and Stop is also a no-op (no goroutine
// is spawned to be torn down).
func (r *CachingResolver) Start(ctx context.Context) {
	if r == nil || r.interval <= 0 {
		return
	}
	go r.loop(ctx)
}

// Stop halts the background refresh and waits for it to finish.
func (r *CachingResolver) Stop() {
	if r == nil || r.interval <= 0 {
		return
	}
	select {
	case <-r.stopCh:
		return
	default:
	}
	close(r.stopCh)
	<-r.stoppedCh
}

func (r *CachingResolver) loop(ctx context.Context) {
	defer close(r.stoppedCh)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-ticker.C:
			if err := r.refresh(ctx); err != nil && r.logger != nil {
				r.logger.Warn("refresh wechat secrets failed", zap.Error(err))
			}
		}
	}
}

func (r *CachingResolver) refresh(ctx context.Context) error {
	values, err := r.source.ListDecrypted(ctx, r.group)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.secrets = values
	r.mu.Unlock()
	return nil
}

// Ensure the configcenter.Service satisfies SecretsSource.
var _ SecretsSource = (*configcenter.Service)(nil)
