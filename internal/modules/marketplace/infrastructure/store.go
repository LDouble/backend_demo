// Package infrastructure persists marketplace aggregates with row-level locking.
package infrastructure

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/modules/marketplace/domain"
	tradedomain "github.com/weouc-plus/campus-platform/internal/modules/trade/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store implements atomic marketplace aggregate operations.
type Store struct{ db *gorm.DB }

// NewStore creates a marketplace persistence adapter.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// CreateListing creates a listing and its ordered images atomically.
func (s *Store) CreateListing(ctx context.Context, ownerID uint64, input domain.ListingInput) (*domain.Listing, error) {
	listing := &domain.Listing{Title: strings.TrimSpace(input.Title), Description: strings.TrimSpace(input.Description), PriceCents: input.PriceCents, Currency: domain.CurrencyCNY, Status: domain.ListingDraft, OwnerId: ownerID, Version: 1}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(listing).Error; err != nil {
			return err
		}
		return replaceImages(tx, listing.ID, input.ImageURLs)
	})
	return listing, err
}

// UpdateListing updates editable listing content and images atomically.
func (s *Store) UpdateListing(ctx context.Context, id, ownerID, version uint64, input domain.ListingInput) (*domain.Listing, error) {
	listing, err := s.mutateListing(ctx, id, ownerID, version, []string{domain.ListingDraft, domain.ListingRejected}, func(tx *gorm.DB, listing *domain.Listing) error {
		listing.Title, listing.Description, listing.PriceCents = strings.TrimSpace(input.Title), strings.TrimSpace(input.Description), input.PriceCents
		listing.Status, listing.RejectionReason = domain.ListingDraft, nil
		if err := tx.Save(listing).Error; err != nil {
			return err
		}
		return replaceImages(tx, listing.ID, input.ImageURLs)
	})
	return listing, err
}

// Submit transitions an owned listing to pending review.
func (s *Store) Submit(ctx context.Context, id, ownerID, version uint64) (*domain.Listing, error) {
	return s.mutateListing(ctx, id, ownerID, version, []string{domain.ListingDraft, domain.ListingRejected}, func(tx *gorm.DB, listing *domain.Listing) error {
		listing.Status, listing.Version = domain.ListingPendingReview, listing.Version+1
		return tx.Save(listing).Error
	})
}

// Withdraw closes an owned open listing.
func (s *Store) Withdraw(ctx context.Context, id, ownerID, version uint64) (*domain.Listing, error) {
	return s.mutateListing(ctx, id, ownerID, version, []string{domain.ListingDraft, domain.ListingPendingReview, domain.ListingPublished, domain.ListingRejected}, func(tx *gorm.DB, listing *domain.Listing) error {
		listing.Status, listing.Version = domain.ListingWithdrawn, listing.Version+1
		return tx.Save(listing).Error
	})
}

// Review records an administrator moderation decision and domain event.
func (s *Store) Review(ctx context.Context, id, adminID, version uint64, approved bool, reason string, now time.Time) (*domain.Listing, error) {
	return s.mutateListing(ctx, id, 0, version, []string{domain.ListingPendingReview}, func(tx *gorm.DB, listing *domain.Listing) error {
		listing.ReviewedBy, listing.ReviewedAt = &adminID, &now
		listing.Version++
		if approved {
			listing.Status, listing.RejectionReason = domain.ListingPublished, nil
		} else {
			listing.Status = domain.ListingRejected
			listing.RejectionReason = pointer(strings.TrimSpace(reason))
		}
		if !approved && listing.RejectionReason == nil {
			return apperror.New(http.StatusBadRequest, "rejection_reason_required", "驳回原因不能为空")
		}
		if err := tx.Save(listing).Error; err != nil {
			return err
		}
		return writeEvent(tx, "listing", listing.ID, "listing.reviewed", fmt.Sprintf("listing.reviewed:%d:%d", listing.ID, listing.Version), listing)
	})
}

// Remove administratively removes a listing and cancels open reservations.
func (s *Store) Remove(ctx context.Context, id, adminID, version uint64, now time.Time) (*domain.Listing, error) {
	return s.mutateListing(ctx, id, 0, version, []string{domain.ListingPendingReview, domain.ListingPublished, domain.ListingReserved}, func(tx *gorm.DB, listing *domain.Listing) error {
		listing.Status, listing.Version = domain.ListingRemoved, listing.Version+1
		if err := tx.Save(listing).Error; err != nil {
			return err
		}
		var reservation domain.MarketplaceReservation
		err := tx.Where("listing_id = ? AND status = ?", listing.ID, domain.ReservationActive).First(&reservation).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			if updateErr := closeReservation(tx, &reservation, tradedomain.StatusCancelled, adminID, "admin_removed", now); updateErr != nil {
				return updateErr
			}
			var order tradedomain.Order
			if findErr := tx.First(&order, reservation.TradeOrderId).Error; findErr != nil {
				return findErr
			}
			key := fmt.Sprintf("order.cancelled:%d:%d", order.ID, order.Version)
			if eventErr := writeEvent(tx, "order", order.ID, "order.cancelled", key, order); eventErr != nil {
				return eventErr
			}
		}
		return writeEvent(tx, "listing", listing.ID, "listing.removed", fmt.Sprintf("listing.removed:%d:%d", listing.ID, listing.Version), map[string]uint64{"listing_id": listing.ID, "admin_id": adminID})
	})
}

// Reserve locks a listing and idempotently creates its buyer order.
func (s *Store) Reserve(ctx context.Context, listingID, buyerID uint64, key string, now time.Time) (*tradedomain.Order, error) {
	if strings.TrimSpace(key) == "" || len(key) > 128 {
		return nil, apperror.New(400, "invalid_idempotency_key", "Idempotency-Key 无效")
	}
	var order tradedomain.Order
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("buyer_id = ? AND idempotency_key = ?", buyerID, key).First(&order).Error; err == nil {
			if order.ResourceType != tradedomain.ResourceListing || order.ResourceId != listingID {
				return apperror.New(409, "idempotency_key_reused", "幂等键已用于其他请求")
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var listing domain.Listing
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&listing, listingID).Error; err != nil {
			return notFound(err, "listing_not_found", "商品不存在")
		}
		if listing.OwnerId == buyerID {
			return apperror.New(403, "self_purchase", "不能购买自己的商品")
		}
		if listing.Status != domain.ListingPublished {
			return apperror.New(409, "listing_unavailable", "商品当前不可购买")
		}
		listing.Status, listing.Version = domain.ListingReserved, listing.Version+1
		if err := tx.Save(&listing).Error; err != nil {
			return err
		}
		expiresAt := now.Add(48 * time.Hour)
		snapshot, marshalErr := json.Marshal(map[string]any{"listing_id": listing.ID, "title": listing.Title, "price_cents": listing.PriceCents})
		if marshalErr != nil {
			return marshalErr
		}
		orderNo, numberErr := newOrderNo()
		if numberErr != nil {
			return numberErr
		}
		order = tradedomain.Order{OrderNo: orderNo, OrderType: tradedomain.OrderTypeMarketplace, ResourceType: tradedomain.ResourceListing, ResourceId: listing.ID, BuyerId: buyerID, SellerId: listing.OwnerId, AmountCents: listing.PriceCents, Currency: listing.Currency, PaymentMode: tradedomain.PaymentOffline, TradeStatus: tradedomain.StatusConfirmed, FulfillmentStatus: tradedomain.FulfillmentNotStarted, TitleSnapshot: listing.Title, ResourceSnapshot: snapshot, IdempotencyKey: key, ExpiresAt: &expiresAt, Version: 1}
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
		reservation := domain.MarketplaceReservation{ListingId: listing.ID, TradeOrderId: order.ID, BuyerId: buyerID, Status: domain.ReservationActive, ExpiresAt: expiresAt, Version: 1}
		if err := tx.Create(&reservation).Error; err != nil {
			return err
		}
		if err := createTransition(tx, &order, "", tradedomain.StatusConfirmed, buyerID, "created", "order.created:"+key); err != nil {
			return err
		}
		return writeEvent(tx, "order", order.ID, "order.created", "order.created:"+key, order)
	})
	return &order, err
}

// Cancel cancels a reserved order and releases its listing.
func (s *Store) Cancel(ctx context.Context, orderID, actorID, version uint64, now time.Time) (*tradedomain.Order, error) {
	return s.finishOrder(ctx, orderID, actorID, version, tradedomain.StatusCancelled, now)
}

// Complete completes a reserved order and marks its listing sold.
func (s *Store) Complete(ctx context.Context, orderID, sellerID, version uint64, now time.Time) (*tradedomain.Order, error) {
	return s.finishOrder(ctx, orderID, sellerID, version, tradedomain.StatusCompleted, now)
}

func (s *Store) finishOrder(ctx context.Context, orderID, actorID, version uint64, target string, now time.Time) (*tradedomain.Order, error) {
	var order tradedomain.Order
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, orderID).Error; err != nil {
			return notFound(err, "order_not_found", "订单不存在")
		}
		if order.Version != version {
			return apperror.New(409, "version_conflict", "订单已被其他请求更新")
		}
		if order.OrderType != tradedomain.OrderTypeMarketplace {
			return apperror.New(409, "wrong_order_type", "订单不属于二手交易")
		}
		if target == tradedomain.StatusCompleted && order.SellerId != actorID {
			return apperror.New(403, "not_seller", "仅卖家可以确认完成")
		}
		if target == tradedomain.StatusCancelled && order.BuyerId != actorID && order.SellerId != actorID {
			return apperror.New(403, "not_order_party", "仅交易双方可以取消订单")
		}
		if !tradedomain.CanTransition(order.TradeStatus, target) {
			return apperror.New(409, "invalid_order_state", "当前订单状态不允许此操作")
		}
		from := order.TradeStatus
		order.TradeStatus, order.Version = target, order.Version+1
		if target == tradedomain.StatusCompleted {
			order.CompletedAt = &now
			order.FulfillmentStatus = tradedomain.FulfillmentDelivered
		} else {
			order.CancelledAt = &now
		}
		if err := tx.Save(&order).Error; err != nil {
			return err
		}
		var reservation domain.MarketplaceReservation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trade_order_id = ? AND status = ?", order.ID, domain.ReservationActive).First(&reservation).Error; err != nil {
			return err
		}
		reservation.Status, reservation.Version = reservationStatus(target), reservation.Version+1
		if err := tx.Save(&reservation).Error; err != nil {
			return err
		}
		listingStatus := domain.ListingSold
		if target == tradedomain.StatusCancelled {
			listingStatus = domain.ListingPublished
		}
		if err := tx.Model(&domain.Listing{}).Where("id = ? AND status = ?", reservation.ListingId, domain.ListingReserved).Updates(map[string]any{"status": listingStatus, "version": gorm.Expr("version + 1")}).Error; err != nil {
			return err
		}
		transitionKey := fmt.Sprintf("order.%s:%d:%d", target, order.ID, order.Version)
		if err := createTransition(tx, &order, from, target, actorID, target, transitionKey); err != nil {
			return err
		}
		return writeEvent(tx, "order", order.ID, "order."+target, transitionKey, order)
	})
	return &order, err
}

// ExpireReservations claims and expires due reservations transactionally.
func (s *Store) ExpireReservations(ctx context.Context, now time.Time) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var reservations []domain.MarketplaceReservation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("status = ? AND expires_at <= ?", domain.ReservationActive, now).Find(&reservations).Error; err != nil {
			return err
		}
		for i := range reservations {
			var order tradedomain.Order
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, reservations[i].TradeOrderId).Error; err != nil {
				return err
			}
			if err := closeReservation(tx, &reservations[i], tradedomain.StatusExpired, 0, "reservation_expired", now); err != nil {
				return err
			}
			if err := tx.First(&order, order.ID).Error; err != nil {
				return err
			}
			if err := tx.Model(&domain.Listing{}).Where("id = ? AND status = ?", reservations[i].ListingId, domain.ListingReserved).Updates(map[string]any{"status": domain.ListingPublished, "version": gorm.Expr("version + 1")}).Error; err != nil {
				return err
			}
			if err := writeEvent(tx, "order", order.ID, "order.expired", fmt.Sprintf("order.expired:%d", order.ID), order); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

func (s *Store) mutateListing(ctx context.Context, id, ownerID, version uint64, states []string, mutate func(*gorm.DB, *domain.Listing) error) (*domain.Listing, error) {
	var listing domain.Listing
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&listing, id).Error; err != nil {
			return notFound(err, "listing_not_found", "商品不存在")
		}
		if ownerID != 0 && listing.OwnerId != ownerID {
			return apperror.New(403, "not_listing_owner", "仅商品所有者可以执行此操作")
		}
		if listing.Version != version {
			return apperror.New(409, "version_conflict", "商品已被其他请求更新")
		}
		allowed := false
		for _, state := range states {
			if listing.Status == state {
				allowed = true
				break
			}
		}
		if !allowed {
			return apperror.New(409, "invalid_listing_state", "当前商品状态不允许此操作")
		}
		return mutate(tx, &listing)
	})
	return &listing, err
}

func replaceImages(tx *gorm.DB, listingID uint64, urls []string) error {
	if err := tx.Where("listing_id = ?", listingID).Delete(&domain.ListingImage{}).Error; err != nil {
		return err
	}
	rows := make([]domain.ListingImage, 0, len(urls))
	for i, rawURL := range urls {
		rows = append(rows, domain.ListingImage{ListingId: listingID, Url: strings.TrimSpace(rawURL), Position: int64(i)})
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Create(&rows).Error
}

func closeReservation(
	tx *gorm.DB,
	reservation *domain.MarketplaceReservation,
	target string,
	actorID uint64,
	reason string,
	now time.Time,
) error {
	var order tradedomain.Order
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, reservation.TradeOrderId).Error; err != nil {
		return err
	}
	if !tradedomain.CanTransition(order.TradeStatus, target) {
		return nil
	}
	from := order.TradeStatus
	order.TradeStatus, order.Version = target, order.Version+1
	if target == tradedomain.StatusCancelled {
		order.CancelledAt = &now
	}
	if err := tx.Save(&order).Error; err != nil {
		return err
	}
	reservation.Status, reservation.Version = reservationStatus(target), reservation.Version+1
	if err := tx.Save(reservation).Error; err != nil {
		return err
	}
	key := fmt.Sprintf("order.%s:%d:%d", target, order.ID, order.Version)
	return createTransition(tx, &order, from, target, actorID, reason, key)
}

func createTransition(
	tx *gorm.DB,
	order *tradedomain.Order,
	from string,
	to string,
	actorID uint64,
	reason string,
	key string,
) error {
	var actor *uint64
	actorType := "system"
	if actorID != 0 {
		actor = &actorID
		actorType = "user"
	}
	transition := tradedomain.OrderTransition{
		OrderId:        order.ID,
		FromStatus:     from,
		ToStatus:       to,
		ActorType:      actorType,
		ActorId:        actor,
		ReasonCode:     pointer(reason),
		IdempotencyKey: key,
	}
	return tx.Create(&transition).Error
}

func reservationStatus(tradeStatus string) string {
	switch tradeStatus {
	case tradedomain.StatusCompleted:
		return domain.ReservationCompleted
	case tradedomain.StatusExpired:
		return domain.ReservationExpired
	default:
		return domain.ReservationCancelled
	}
}

func newOrderNo() (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate order number: %w", err)
	}
	return fmt.Sprintf("TRD%x", random), nil
}

func writeEvent(tx *gorm.DB, aggregate string, id uint64, eventType, key string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return tx.Create(&domainevent.Event{AggregateType: aggregate, AggregateID: id, EventType: eventType, PayloadVersion: 1, Payload: data, IdempotencyKey: key, Status: domainevent.StatusPending, AvailableAt: time.Now().UTC()}).Error
}
func pointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func notFound(err error, code, message string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperror.New(404, code, message)
	}
	return err
}
