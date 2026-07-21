package permission

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const policyReloadInterval = 5 * time.Second

const policyClaimLease = 30 * time.Second

type policyOutbox struct {
	ID           uint64 `gorm:"primaryKey;autoIncrement"`
	Version      string `gorm:"size:64;not null;uniqueIndex"`
	Attempts     int64  `gorm:"not null"`
	DispatchedAt *time.Time
	LockedAt     *time.Time
	LockedBy     string `gorm:"size:64"`
	CreatedAt    time.Time
}

func (policyOutbox) TableName() string { return "permission_policy_outbox" }

// PolicyNotifier broadcasts durable policy versions between service instances.
type PolicyNotifier interface {
	Publish(context.Context, string) error
	Run(context.Context, func(string) error) error
}

type policySync struct {
	cancel     context.CancelFunc
	wait       sync.WaitGroup
	instanceID string
	pending    atomic.Int64
	published  atomic.Uint64
	failures   atomic.Uint64
}

// PolicySyncStats is a point-in-time synchronization health snapshot.
type PolicySyncStats struct {
	Pending   int64  `json:"pending"`
	Published uint64 `json:"published"`
	Failures  uint64 `json:"failures"`
}

// SyncStats returns policy relay counters for metrics adapters and diagnostics.
func (s *Service) SyncStats() PolicySyncStats {
	return PolicySyncStats{Pending: s.sync.pending.Load(), Published: s.sync.published.Load(), Failures: s.sync.failures.Load()}
}

// StartSync starts Redis notification consumption, outbox relay and periodic reload.
func (s *Service) StartSync(parent context.Context, notifier PolicyNotifier) {
	ctx, cancel := context.WithCancel(parent)
	s.sync.cancel = cancel
	s.sync.instanceID = uuid.NewString()
	s.sync.wait.Add(2)
	go func() {
		defer s.sync.wait.Done()
		s.runReloadLoop(ctx, notifier)
	}()
	go func() {
		defer s.sync.wait.Done()
		s.runOutboxRelay(ctx, notifier)
	}()
}

// StopSync stops policy background work and waits for every loop to exit.
func (s *Service) StopSync() {
	if s.sync.cancel != nil {
		s.sync.cancel()
	}
	s.sync.wait.Wait()
}

func (s *Service) runReloadLoop(ctx context.Context, notifier PolicyNotifier) {
	ticker := time.NewTicker(policyReloadInterval)
	defer ticker.Stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			if err := s.reloadPolicy(ctx); err != nil {
				s.sync.failures.Add(1)
				s.log.Error("reload permissions before notifier subscription", zap.Error(err))
			}
			err := notifier.Run(ctx, func(version string) error {
				if reloadErr := s.reloadPolicy(ctx); reloadErr != nil {
					s.sync.failures.Add(1)
					s.log.Error("reload permissions from notification", zap.Error(reloadErr), zap.String("policy_version", version))
					return reloadErr
				}
				return nil
			})
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				s.sync.failures.Add(1)
				s.log.Warn("permission notifier disconnected", zap.Error(err))
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			<-done
			return
		case <-ticker.C:
			if err := s.reloadPolicy(ctx); err != nil {
				s.sync.failures.Add(1)
				s.log.Error("periodic permission reload", zap.Error(err))
			}
		}
	}
}

func (s *Service) runOutboxRelay(ctx context.Context, notifier PolicyNotifier) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		// The durable row remains pending when publishing fails, so the next tick retries it.
		if err := s.relayPolicyChanges(ctx, notifier); err != nil && ctx.Err() == nil {
			s.sync.failures.Add(1)
			s.log.Warn("relay permission policy changes", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) relayPolicyChanges(ctx context.Context, notifier PolicyNotifier) error {
	rows := []policyOutbox{}
	leaseBefore := time.Now().UTC().Add(-policyClaimLease)
	if err := s.db.WithContext(ctx).
		Where("dispatched_at IS NULL AND (locked_at IS NULL OR locked_at < ?)", leaseBefore).
		Order("id").Limit(100).Find(&rows).Error; err != nil {
		return fmt.Errorf("list permission policy outbox: %w", err)
	}
	s.sync.pending.Store(int64(len(rows)))
	for _, row := range rows {
		now := time.Now().UTC()
		claim := s.db.WithContext(ctx).Model(&policyOutbox{}).
			Where("id = ? AND dispatched_at IS NULL AND (locked_at IS NULL OR locked_at < ?)", row.ID, leaseBefore).
			Updates(map[string]any{"locked_at": now, "locked_by": s.sync.instanceID})
		if claim.Error != nil {
			return fmt.Errorf("claim permission policy outbox: %w", claim.Error)
		}
		if claim.RowsAffected != 1 {
			continue
		}
		if err := notifier.Publish(ctx, row.Version); err != nil {
			updateErr := s.db.WithContext(ctx).Model(&policyOutbox{}).
				Where("id = ? AND dispatched_at IS NULL AND locked_by = ?", row.ID, s.sync.instanceID).
				Updates(map[string]any{"attempts": gorm.Expr("attempts + 1"), "locked_at": nil, "locked_by": ""}).Error
			if updateErr != nil {
				return fmt.Errorf("release failed permission policy claim: %w", updateErr)
			}
			return fmt.Errorf("publish permission policy version: %w", err)
		}
		if err := s.db.WithContext(ctx).Model(&policyOutbox{}).
			Where("id = ? AND dispatched_at IS NULL AND locked_by = ?", row.ID, s.sync.instanceID).
			Updates(map[string]any{"attempts": gorm.Expr("attempts + 1"), "dispatched_at": now, "locked_at": nil, "locked_by": ""}).Error; err != nil {
			return fmt.Errorf("complete permission policy outbox: %w", err)
		}
		s.sync.published.Add(1)
	}
	return nil
}

func (s *Service) reloadPolicy(ctx context.Context) error {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	enforcer, err := newPolicyEnforcer(ctx, s.db)
	if err != nil {
		return fmt.Errorf("reload permission policy: %w", err)
	}
	s.enforcer.Store(enforcer)
	return nil
}

// ReloadPolicy reloads the committed policy snapshot into the local enforcer.
func (s *Service) ReloadPolicy(ctx context.Context) error { return s.reloadPolicy(ctx) }

func recordPolicyChange(tx *gorm.DB) error {
	version := uuid.NewString()
	return tx.Create(&policyOutbox{Version: version}).Error
}
