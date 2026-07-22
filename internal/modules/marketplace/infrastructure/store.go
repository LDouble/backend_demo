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
	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	"github.com/weouc-plus/campus-platform/internal/core/privacy"
	"github.com/weouc-plus/campus-platform/internal/modules/marketplace/domain"
	tradedomain "github.com/weouc-plus/campus-platform/internal/modules/trade/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store implements atomic marketplace aggregate operations.
type Store struct {
	db     *gorm.DB
	cipher *configcenter.Cipher
}

// NewStore creates a marketplace persistence adapter.
func NewStore(db *gorm.DB, ciphers ...*configcenter.Cipher) *Store {
	store := &Store{db: db}
	if len(ciphers) > 0 {
		store.cipher = ciphers[0]
	}
	return store
}

// CreateListing creates a listing and its ordered images atomically.
func (s *Store) CreateListing(ctx context.Context, ownerID uint64, input domain.ListingInput) (*domain.Listing, error) {
	listing := &domain.Listing{Title: strings.TrimSpace(input.Title), Description: strings.TrimSpace(input.Description), PriceCents: input.PriceCents, Currency: domain.CurrencyCNY, Status: domain.ListingDraft, OwnerId: ownerID, ContactType: strings.TrimSpace(input.Contact.Type), Version: 1}
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(listing).Error; err != nil {
			return err
		}
		ciphertext, err := s.encryptContact(input.Contact.Value, listingContactAAD(listing.ID))
		if err != nil {
			return err
		}
		listing.ContactCiphertext = ciphertext
		if err := tx.Model(listing).Update("contact_ciphertext", ciphertext).Error; err != nil {
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
		if input.Contact.Provided {
			ciphertext, err := s.encryptContact(input.Contact.Value, listingContactAAD(listing.ID))
			if err != nil {
				return err
			}
			listing.ContactType, listing.ContactCiphertext = strings.TrimSpace(input.Contact.Type), ciphertext
		}
		result := tx.Model(&domain.Listing{}).
			Where("id = ? AND version = ?", listing.ID, version).
			Updates(map[string]any{
				"title": listing.Title, "description": listing.Description,
				"price_cents": listing.PriceCents, "status": listing.Status,
				"rejection_reason": listing.RejectionReason,
				"contact_type":     listing.ContactType, "contact_ciphertext": listing.ContactCiphertext,
				"version": gorm.Expr("version + 1"),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return apperror.New(http.StatusConflict, "version_conflict", "商品已被其他请求更新")
		}
		listing.Version++
		if err := replaceImages(tx, listing.ID, input.ImageURLs); err != nil {
			return err
		}
		return nil
	})
	return listing, err
}

// Contact returns either plaintext for an active trade participant or a masked value.
func (s *Store) Contact(ctx context.Context, listing *domain.Listing, viewerID uint64) (domain.ContactDetails, error) {
	value, err := s.decryptContact(listing.ContactCiphertext, listingContactAAD(listing.ID))
	if err != nil {
		return domain.ContactDetails{}, err
	}
	if value == "" {
		return domain.ContactDetails{Type: listing.ContactType}, nil
	}
	if listing.Status != domain.ListingSold && listing.Status != domain.ListingWithdrawn && listing.Status != domain.ListingRemoved {
		if viewerID == listing.OwnerId {
			return domain.ContactDetails{Type: listing.ContactType, Value: value}, nil
		}
		var count int64
		if err := idempotency.DB(ctx, s.db).Model(&tradedomain.Order{}).Where(
			"resource_type = ? AND resource_id = ? AND buyer_id = ? AND trade_status = ?",
			tradedomain.ResourceListing,
			listing.ID,
			viewerID,
			tradedomain.StatusConfirmed,
		).Count(&count).Error; err != nil {
			return domain.ContactDetails{}, err
		}
		if count > 0 {
			return domain.ContactDetails{Type: listing.ContactType, Value: value}, nil
		}
	}
	return domain.ContactDetails{Type: listing.ContactType, Value: privacy.MaskContact(value)}, nil
}

func (s *Store) encryptContact(value, aad string) (string, error) {
	if s.cipher == nil {
		return "", fmt.Errorf("contact cipher is not configured")
	}
	return s.cipher.Encrypt(strings.TrimSpace(value), aad)
}

func (s *Store) decryptContact(ciphertext, aad string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	if s.cipher == nil {
		return "", fmt.Errorf("contact cipher is not configured")
	}
	return s.cipher.Decrypt(ciphertext, aad)
}

func listingContactAAD(id uint64) string { return fmt.Sprintf("marketplace-listing:%d", id) }

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
			trimmedReason := strings.TrimSpace(reason)
			if trimmedReason == "" {
				return apperror.New(http.StatusBadRequest, "rejection_reason_required", "驳回原因不能为空")
			}
			listing.Status = domain.ListingRejected
			listing.RejectionReason = pointer(trimmedReason)
		}
		if err := tx.Save(listing).Error; err != nil {
			return err
		}
		return writeEvent(tx, "listing", listing.ID, "listing.reviewed", fmt.Sprintf("listing.reviewed:%d:%d", listing.ID, listing.Version), listingEventPayload(listing))
	})
}

// Remove administratively removes a listing and cancels an open reservation.
// When an order exists, all paths use order -> reservation -> listing locking.
func (s *Store) Remove(ctx context.Context, id, adminID, version uint64, now time.Time) (*domain.Listing, error) {
	var listing domain.Listing
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		var reservation domain.MarketplaceReservation
		err := tx.Where("listing_id = ? AND status = ?", id, domain.ReservationActive).First(&reservation).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var order tradedomain.Order
		hasReservation := err == nil
		if err == nil {
			if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, reservation.TradeOrderId).Error; err != nil {
				return err
			}
			err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ? AND status = ?", reservation.ID, domain.ReservationActive).
				First(&reservation).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				hasReservation = false
			} else if err != nil {
				return err
			}
		}
		if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&listing, id).Error; err != nil {
			return notFound(err, "listing_not_found", "商品不存在")
		}
		if listing.Version != version {
			return apperror.New(409, "version_conflict", "商品已被其他请求更新")
		}
		if listing.Status != domain.ListingPendingReview && listing.Status != domain.ListingPublished && listing.Status != domain.ListingReserved {
			return apperror.New(409, "invalid_listing_state", "商品当前状态不允许此操作")
		}
		listing.Status, listing.Version = domain.ListingRemoved, listing.Version+1
		if err = tx.Save(&listing).Error; err != nil {
			return err
		}
		if hasReservation {
			changed, closeErr := closeReservationWithOrder(tx, &reservation, &order, tradedomain.StatusCancelled, adminID, "admin_removed", now)
			if closeErr != nil {
				return closeErr
			}
			if changed {
				key := fmt.Sprintf("order.cancelled:%d:%d", order.ID, order.Version)
				if eventErr := writeEvent(tx, "order", order.ID, "order.cancelled", key, order); eventErr != nil {
					return eventErr
				}
			}
		}
		return writeEvent(tx, "listing", listing.ID, "listing.removed", fmt.Sprintf("listing.removed:%d:%d", listing.ID, listing.Version), map[string]uint64{"listing_id": listing.ID, "admin_id": adminID})
	})
	return &listing, err
}

// Reserve locks a listing and idempotently creates its buyer order.
func (s *Store) Reserve(ctx context.Context, listingID, buyerID uint64, key string, now time.Time) (*tradedomain.Order, error) {
	if strings.TrimSpace(key) == "" || len(key) > 128 {
		return nil, apperror.New(400, "invalid_idempotency_key", "Idempotency-Key 无效")
	}
	var order tradedomain.Order
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
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
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
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
		var listing domain.Listing
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&listing, reservation.ListingId).Error; err != nil {
			return err
		}
		listingStatus := domain.ListingSold
		if target == tradedomain.StatusCancelled {
			listingStatus = domain.ListingPublished
		}
		if err := tx.Model(&listing).Where("status = ?", domain.ListingReserved).Updates(map[string]any{"status": listingStatus, "version": gorm.Expr("version + 1")}).Error; err != nil {
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
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		var reservations []domain.MarketplaceReservation
		if err := tx.Where("status = ? AND expires_at <= ?", domain.ReservationActive, now).Order("id").Limit(100).Find(&reservations).Error; err != nil {
			return err
		}
		for i := range reservations {
			var order tradedomain.Order
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, reservations[i].TradeOrderId).Error; err != nil {
				return err
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ? AND status = ? AND expires_at <= ?", reservations[i].ID, domain.ReservationActive, now).
				First(&reservations[i]).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			} else if err != nil {
				return err
			}
			var listing domain.Listing
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&listing, reservations[i].ListingId).Error; err != nil {
				return err
			}
			changed, closeErr := closeReservationWithOrder(tx, &reservations[i], &order, tradedomain.StatusExpired, 0, "reservation_expired", now)
			if closeErr != nil {
				return closeErr
			}
			if !changed {
				continue
			}
			if err := tx.Model(&listing).Where("status = ?", domain.ListingReserved).Updates(map[string]any{"status": domain.ListingPublished, "version": gorm.Expr("version + 1")}).Error; err != nil {
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
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
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

func closeReservationWithOrder(
	tx *gorm.DB,
	reservation *domain.MarketplaceReservation,
	order *tradedomain.Order,
	target string,
	actorID uint64,
	reason string,
	now time.Time,
) (bool, error) {
	if !tradedomain.CanTransition(order.TradeStatus, target) {
		return false, nil
	}
	from := order.TradeStatus
	order.TradeStatus, order.Version = target, order.Version+1
	if target == tradedomain.StatusCancelled {
		order.CancelledAt = &now
	}
	if err := tx.Save(order).Error; err != nil {
		return false, err
	}
	reservation.Status, reservation.Version = reservationStatus(target), reservation.Version+1
	if err := tx.Save(reservation).Error; err != nil {
		return false, err
	}
	key := fmt.Sprintf("order.%s:%d:%d", target, order.ID, order.Version)
	if err := createTransition(tx, order, from, target, actorID, reason, key); err != nil {
		return false, err
	}
	return true, nil
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
	return domainevent.WriteWithKey(tx, aggregate, id, eventType, key, payload)
}

func listingEventPayload(listing *domain.Listing) map[string]any {
	return map[string]any{"listing_id": listing.ID, "status": listing.Status, "version": listing.Version}
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
