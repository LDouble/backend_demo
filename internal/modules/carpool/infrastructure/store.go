// Package infrastructure persists carpool aggregates with row-level locking.
package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	"github.com/weouc-plus/campus-platform/internal/modules/carpool/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store owns the locked aggregate mutations. Locking is intentionally expressed
// here because GORM Gen does not expose SELECT ... FOR UPDATE for this aggregate.
type Store struct {
	db     *gorm.DB
	cipher *configcenter.Cipher
}

// NewStore creates a carpool persistence adapter.
func NewStore(db *gorm.DB, cipher *configcenter.Cipher) *Store { return &Store{db: db, cipher: cipher} }

// RevealContact decrypts the organizer contact for internal use.
func (s *Store) RevealContact(trip *domain.Trip) (string, error) {
	return s.cipher.Decrypt(trip.ContactCiphertext, contactAAD(trip.ID))
}

// CreateTrip inserts a new trip and encrypts the organizer contact.
func (s *Store) CreateTrip(ctx context.Context, organizer uint64, in domain.TripInput, _ time.Time) (*domain.Trip, error) {
	trip := &domain.Trip{Title: strings.TrimSpace(in.Title), Origin: strings.TrimSpace(in.Origin), Destination: strings.TrimSpace(in.Destination), DepartureAt: in.DepartureAt.UTC(), TotalSeats: in.TotalSeats, Status: domain.TripOpen, OrganizerId: organizer, ContactType: strings.TrimSpace(in.ContactType), Version: 1}
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(trip).Error; err != nil {
			return err
		}
		ciphertext, err := s.cipher.Encrypt(strings.TrimSpace(in.Contact), contactAAD(trip.ID))
		if err != nil {
			return err
		}
		trip.ContactCiphertext = ciphertext
		if err := tx.Save(trip).Error; err != nil {
			return err
		}
		return event(tx, trip, "carpool.created")
	})
	return trip, err
}

// GetTrip returns one trip and whether the viewer may see plaintext contact.
func (s *Store) GetTrip(ctx context.Context, id, viewer uint64) (*domain.Trip, bool, error) {
	var trip domain.Trip
	if err := idempotency.DB(ctx, s.db).First(&trip, id).Error; err != nil {
		return nil, false, notFound(err)
	}
	visible := trip.OrganizerId == viewer
	if !visible && trip.Status != domain.TripCancelled && trip.Status != domain.TripCompleted {
		var p domain.Participant
		err := idempotency.DB(ctx, s.db).Where("trip_id = ? AND user_id = ? AND status = ?", id, viewer, domain.ParticipantJoined).First(&p).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, err
		}
		visible = err == nil
	}
	return &trip, visible, nil
}

// SearchTrips returns public trips that have not departed.
func (s *Store) SearchTrips(ctx context.Context, search domain.Search, page, size int, now time.Time) ([]domain.Trip, int64, error) {
	rows := []domain.Trip{}
	base := idempotency.DB(ctx, s.db).Model(&domain.Trip{}).Where("status IN ? AND departure_at > ?", []string{domain.TripOpen, domain.TripFull}, now)
	if v := strings.TrimSpace(search.Origin); v != "" {
		base = base.Where("origin LIKE ?", "%"+v+"%")
	}
	if v := strings.TrimSpace(search.Destination); v != "" {
		base = base.Where("destination LIKE ?", "%"+v+"%")
	}
	if search.DepartureDate != nil {
		start := search.DepartureDate.UTC()
		base = base.Where("departure_at >= ? AND departure_at < ?", start, start.AddDate(0, 0, 1))
	}
	if search.SeatsNeeded > 0 {
		base = base.Where("total_seats - occupied_seats >= ?", search.SeatsNeeded)
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := base.Order("departure_at ASC, id ASC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// Join adds a participant and reserves one seat atomically.
func (s *Store) Join(ctx context.Context, id, user, version uint64, now time.Time) (*domain.Trip, error) {
	var trip domain.Trip
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&trip, id).Error; err != nil {
			return notFound(err)
		}
		if trip.Version != version {
			return conflict("version_conflict", "行程已被其他请求更新")
		}
		if trip.OrganizerId == user {
			return apperror.New(403, "self_join", "不能加入自己发起的行程")
		}
		if trip.Status != domain.TripOpen && trip.Status != domain.TripFull {
			return conflict("trip_unavailable", "行程当前不可加入")
		}
		if !trip.DepartureAt.After(now) {
			return conflict("trip_departed", "行程已经出发")
		}
		if trip.OccupiedSeats >= trip.TotalSeats {
			return conflict("trip_full", "座位已满")
		}
		var p domain.Participant
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trip_id = ? AND user_id = ?", id, user).First(&p).Error
		if err == nil && p.Status == domain.ParticipantJoined {
			return conflict("already_joined", "已加入该行程")
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			p = domain.Participant{TripId: id, UserId: user, Status: domain.ParticipantJoined, JoinedAt: now, Version: 1}
			if err := tx.Create(&p).Error; err != nil {
				return err
			}
		} else {
			p.Status, p.JoinedAt, p.CancelledAt, p.Version = domain.ParticipantJoined, now, nil, p.Version+1
			if err := tx.Save(&p).Error; err != nil {
				return err
			}
		}
		trip.OccupiedSeats++
		trip.Version++
		if trip.OccupiedSeats == trip.TotalSeats {
			trip.Status = domain.TripFull
		}
		if err := tx.Save(&trip).Error; err != nil {
			return err
		}
		return event(tx, &trip, "carpool.joined")
	})
	return &trip, err
}

// Leave removes a participant and releases one occupied seat.
func (s *Store) Leave(ctx context.Context, id, user, version uint64, now time.Time) (*domain.Trip, error) {
	return s.changeParticipant(ctx, id, user, version, now, false)
}

// Cancel marks an organizer-owned trip as cancelled.
func (s *Store) Cancel(ctx context.Context, id, user, version uint64, now time.Time) (*domain.Trip, error) {
	var trip domain.Trip
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&trip, id).Error; err != nil {
			return notFound(err)
		}
		if trip.Version != version {
			return conflict("version_conflict", "行程已被其他请求更新")
		}
		if trip.OrganizerId != user {
			return apperror.New(403, "not_organizer", "仅发起人可以取消行程")
		}
		if (trip.Status != domain.TripOpen && trip.Status != domain.TripFull) || !trip.DepartureAt.After(now) {
			return conflict("trip_unavailable", "当前行程不能取消")
		}
		trip.Status = domain.TripCancelled
		trip.Version++
		if err := tx.Save(&trip).Error; err != nil {
			return err
		}
		return event(tx, &trip, "carpool.cancelled")
	})
	return &trip, err
}
func (s *Store) changeParticipant(ctx context.Context, id, user, version uint64, now time.Time, _ bool) (*domain.Trip, error) {
	var trip domain.Trip
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&trip, id).Error; err != nil {
			return notFound(err)
		}
		if trip.Version != version {
			return conflict("version_conflict", "行程已被其他请求更新")
		}
		if (trip.Status != domain.TripOpen && trip.Status != domain.TripFull) || !trip.DepartureAt.After(now) {
			return conflict("trip_unavailable", "当前行程不能退出")
		}
		var p domain.Participant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trip_id = ? AND user_id = ?", id, user).First(&p).Error; err != nil || p.Status != domain.ParticipantJoined {
			return apperror.New(403, "not_participant", "仅有效参与者可以退出")
		}
		p.Status, p.CancelledAt, p.Version = domain.ParticipantLeft, &now, p.Version+1
		if err := tx.Save(&p).Error; err != nil {
			return err
		}
		trip.OccupiedSeats--
		trip.Status = domain.TripOpen
		trip.Version++
		if err := tx.Save(&trip).Error; err != nil {
			return err
		}
		return event(tx, &trip, "carpool.left")
	})
	return &trip, err
}

// CompleteDue marks departed active trips completed.
func (s *Store) CompleteDue(ctx context.Context, now time.Time) (int64, error) {
	var rows []domain.Trip
	if err := idempotency.DB(ctx, s.db).Where("status IN ? AND departure_at <= ?", []string{domain.TripOpen, domain.TripFull}, now).Find(&rows).Error; err != nil {
		return 0, err
	}
	var completed int64
	for _, row := range rows {
		changed := false
		err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
			var trip domain.Trip
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&trip, row.ID).Error; err != nil {
				return err
			}
			if (trip.Status != domain.TripOpen && trip.Status != domain.TripFull) || trip.DepartureAt.After(now) {
				return nil
			}
			trip.Status = domain.TripCompleted
			trip.Version++
			if err := tx.Save(&trip).Error; err != nil {
				return err
			}
			if err := event(tx, &trip, "carpool.completed"); err != nil {
				return err
			}
			changed = true
			return nil
		})
		if err != nil {
			return completed, err
		}
		if changed {
			completed++
		}
	}
	return completed, nil
}
func contactAAD(id uint64) string { return fmt.Sprintf("carpool-trip:%d", id) }
func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperror.New(404, "carpool_trip_not_found", "拼车行程不存在")
	}
	return err
}
func conflict(code, msg string) error { return apperror.New(409, code, msg) }
func event(tx *gorm.DB, trip *domain.Trip, kind string) error {
	payload := map[string]any{"trip_id": trip.ID, "status": trip.Status, "version": trip.Version}
	key := fmt.Sprintf("carpool.%s:%d:%d", kind, trip.ID, trip.Version)
	return domainevent.WriteWithKey(tx, "carpool_trip", trip.ID, kind, key, payload)
}
