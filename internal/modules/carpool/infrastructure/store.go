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
	trip := &domain.Trip{Title: strings.TrimSpace(in.Title), Origin: strings.TrimSpace(in.Origin), Destination: strings.TrimSpace(in.Destination), DepartureAt: in.DepartureAt.UTC(), TotalSeats: in.TotalSeats, Status: domain.TripOpen, ReviewStatus: domain.ReviewPending, OrganizerId: organizer, ContactType: strings.TrimSpace(in.ContactType), Version: 1}
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
	if trip.ReviewStatus != domain.ReviewApproved && trip.OrganizerId != viewer {
		return nil, false, notFound(gorm.ErrRecordNotFound)
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
	base := idempotency.DB(ctx, s.db).Model(&domain.Trip{}).Where(
		"status IN ? AND review_status = ? AND departure_at > ?",
		[]string{domain.TripOpen, domain.TripFull},
		domain.ReviewApproved,
		now,
	)
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

// UpdateTrip changes an organizer-owned, unoccupied trip and requires re-review.
func (s *Store) UpdateTrip(
	ctx context.Context,
	id,
	organizer,
	version uint64,
	in domain.TripInput,
	_ time.Time,
) (*domain.Trip, error) {
	return s.mutateReviewableTrip(ctx, id, organizer, version, func(tx *gorm.DB, trip *domain.Trip) error {
		if trip.OccupiedSeats != 0 {
			return conflict("trip_has_participants", "已有参与者的行程不可修改")
		}
		trip.Title = strings.TrimSpace(in.Title)
		trip.Origin = strings.TrimSpace(in.Origin)
		trip.Destination = strings.TrimSpace(in.Destination)
		trip.DepartureAt = in.DepartureAt.UTC()
		trip.TotalSeats = in.TotalSeats
		if in.ContactProvided {
			ciphertext, err := s.cipher.Encrypt(strings.TrimSpace(in.Contact), contactAAD(trip.ID))
			if err != nil {
				return err
			}
			trip.ContactType = strings.TrimSpace(in.ContactType)
			trip.ContactCiphertext = ciphertext
		}
		trip.ReviewStatus = domain.ReviewDraft
		trip.ReviewReason = nil
		trip.ReviewedBy = nil
		trip.ReviewedAt = nil
		trip.Version++
		if err := tx.Save(trip).Error; err != nil {
			return err
		}
		return event(tx, trip, "carpool.updated")
	})
}

// ListAdmin returns trips matching moderation filters.
func (s *Store) ListAdmin(
	ctx context.Context,
	search domain.AdminSearch,
	page,
	size int,
) ([]domain.Trip, int64, error) {
	rows := []domain.Trip{}
	base := idempotency.DB(ctx, s.db).Model(&domain.Trip{})
	if keyword := strings.TrimSpace(search.Keyword); keyword != "" {
		pattern := "%" + keyword + "%"
		base = base.Where(
			"(title LIKE ? OR origin LIKE ? OR destination LIKE ?)",
			pattern,
			pattern,
			pattern,
		)
	}
	if status := strings.TrimSpace(search.Status); status != "" {
		base = base.Where("status = ?", status)
	}
	if reviewStatus := strings.TrimSpace(search.ReviewStatus); reviewStatus != "" {
		base = base.Where("review_status = ?", reviewStatus)
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := base.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// SubmitReview moves an edited trip back into moderation.
func (s *Store) SubmitReview(ctx context.Context, id, organizer, version uint64) (*domain.Trip, error) {
	return s.mutateReviewableTrip(ctx, id, organizer, version, func(tx *gorm.DB, trip *domain.Trip) error {
		if trip.ReviewStatus != domain.ReviewDraft && trip.ReviewStatus != domain.ReviewRejected {
			return conflict("invalid_carpool_review_state", "当前行程不可重新提交审核")
		}
		trip.ReviewStatus = domain.ReviewPending
		trip.ReviewReason = nil
		trip.ReviewedBy = nil
		trip.ReviewedAt = nil
		trip.Version++
		if err := tx.Save(trip).Error; err != nil {
			return err
		}
		return event(tx, trip, "carpool.review_submitted")
	})
}

// Review records an administrator moderation decision.
func (s *Store) Review(
	ctx context.Context,
	id,
	adminID,
	version uint64,
	approved bool,
	reason string,
	now time.Time,
) (*domain.Trip, error) {
	return s.mutateReviewableTrip(ctx, id, 0, version, func(tx *gorm.DB, trip *domain.Trip) error {
		if trip.ReviewStatus != domain.ReviewPending {
			return conflict("invalid_carpool_review_state", "行程当前不在待审核状态")
		}
		trimmedReason := strings.TrimSpace(reason)
		if !approved && trimmedReason == "" {
			return apperror.New(400, "rejection_reason_required", "驳回原因不能为空")
		}
		trip.ReviewStatus = domain.ReviewApproved
		trip.ReviewReason = nil
		if !approved {
			trip.ReviewStatus = domain.ReviewRejected
			trip.ReviewReason = &trimmedReason
		}
		trip.ReviewedBy = &adminID
		trip.ReviewedAt = &now
		trip.Version++
		if err := tx.Save(trip).Error; err != nil {
			return err
		}
		return event(tx, trip, "carpool.reviewed")
	})
}

// RevokeReview hides an approved, unoccupied trip and returns it to moderation.
func (s *Store) RevokeReview(
	ctx context.Context,
	id,
	adminID,
	version uint64,
	reason string,
	now time.Time,
) (*domain.Trip, error) {
	return s.mutateReviewableTrip(ctx, id, 0, version, func(tx *gorm.DB, trip *domain.Trip) error {
		trimmedReason := strings.TrimSpace(reason)
		if trimmedReason == "" {
			return apperror.New(400, "revoke_reason_required", "撤销原因不能为空")
		}
		if trip.ReviewStatus != domain.ReviewApproved || trip.OccupiedSeats != 0 {
			return conflict("invalid_carpool_review_state", "仅可撤销未成行的已通过审核")
		}
		trip.ReviewStatus = domain.ReviewPending
		trip.ReviewReason = &trimmedReason
		trip.ReviewedBy = &adminID
		trip.ReviewedAt = &now
		trip.Version++
		if err := tx.Save(trip).Error; err != nil {
			return err
		}
		return event(tx, trip, "carpool.review_revoked")
	})
}

func (s *Store) mutateReviewableTrip(
	ctx context.Context,
	id,
	organizer,
	version uint64,
	change func(*gorm.DB, *domain.Trip) error,
) (*domain.Trip, error) {
	var trip domain.Trip
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&trip, id).Error; err != nil {
			return notFound(err)
		}
		if organizer != 0 && trip.OrganizerId != organizer {
			return apperror.New(403, "not_organizer", "仅发起人可以执行此操作")
		}
		if trip.Version != version {
			return conflict("version_conflict", "行程已被其他请求更新")
		}
		if trip.Status != domain.TripOpen || !trip.DepartureAt.After(time.Now().UTC()) {
			return conflict("trip_unavailable", "当前行程不可执行审核操作")
		}
		return change(tx, &trip)
	})
	return &trip, err
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
		if trip.ReviewStatus != domain.ReviewApproved {
			return conflict("trip_unavailable", "行程尚未通过审核")
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
	return s.changeParticipant(ctx, id, user, version, now)
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
func (s *Store) changeParticipant(ctx context.Context, id, user, version uint64, now time.Time) (*domain.Trip, error) {
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
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trip_id = ? AND user_id = ?", id, user).First(&p).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if errors.Is(err, gorm.ErrRecordNotFound) || p.Status != domain.ParticipantJoined {
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
	if err := idempotency.DB(ctx, s.db).
		Where("status IN ? AND departure_at <= ?", []string{domain.TripOpen, domain.TripFull}, now).
		Order("id").
		Limit(100).
		Find(&rows).Error; err != nil {
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
