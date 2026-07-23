// Package infrastructure persists errand aggregates with row-level locking.
package infrastructure

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	"github.com/weouc-plus/campus-platform/internal/core/privacy"
	platformquery "github.com/weouc-plus/campus-platform/internal/infrastructure/mysql/query"
	"github.com/weouc-plus/campus-platform/internal/modules/errand/domain"
	tradedomain "github.com/weouc-plus/campus-platform/internal/modules/trade/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store persists errand aggregates and their trade-order workflow.
type Store struct {
	db     *gorm.DB
	cipher *configcenter.Cipher
}

// NewStore creates an errand persistence adapter.
func NewStore(db *gorm.DB, ciphers ...*configcenter.Cipher) *Store {
	store := &Store{db: db}
	if len(ciphers) > 0 {
		store.cipher = ciphers[0]
	}
	return store
}

// Create inserts a new errand task and encrypts its contact details.
func (s *Store) Create(ctx context.Context, requester uint64, input domain.TaskInput) (*domain.Task, error) {
	task := &domain.Task{Title: strings.TrimSpace(input.Title), Description: strings.TrimSpace(input.Description), RewardCents: input.RewardCents, Currency: domain.CurrencyCNY, PickupLocation: strings.TrimSpace(input.PickupLocation), DropoffLocation: strings.TrimSpace(input.DropoffLocation), Deadline: input.Deadline.UTC(), Status: domain.TaskOpen, ReviewStatus: domain.ReviewPending, RequesterId: requester, ContactType: strings.TrimSpace(input.Contact.Type), Version: 1}
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		ciphertext, err := s.encryptContact(input.Contact.Value, taskContactAAD(task.ID))
		if err != nil {
			return err
		}
		task.ContactCiphertext = ciphertext
		if err := tx.Model(task).Update("contact_ciphertext", ciphertext).Error; err != nil {
			return err
		}
		return taskEvent(tx, task, "errand.created", fmt.Sprintf("errand.created:%d", task.ID))
	})
	return task, err
}

// GetVisible returns one approved task, or any task owned by the viewer.
func (s *Store) GetVisible(ctx context.Context, id, viewerID uint64) (*domain.Task, error) {
	var task domain.Task
	q := platformquery.Use(idempotency.DB(ctx, s.db)).Task
	dao := q.WithContext(ctx).Where(q.ID.Eq(id))
	if viewerID == 0 {
		dao = dao.Where(q.ReviewStatus.Eq(domain.ReviewApproved))
	} else {
		dao = dao.Where(q.ReviewStatus.Eq(domain.ReviewApproved)).Or(q.RequesterId.Eq(viewerID))
	}
	value, err := dao.First()
	if err == nil {
		task = *value
	}
	return &task, taskNotFound(err)
}

// ListOpen returns currently open tasks before their deadlines.
func (s *Store) ListOpen(ctx context.Context, page, size int, now time.Time) ([]domain.Task, int64, error) {
	var rows []domain.Task
	q := platformquery.Use(idempotency.DB(ctx, s.db)).Task
	dao := q.WithContext(ctx).Where(
		q.Status.Eq(domain.TaskOpen),
		q.ReviewStatus.Eq(domain.ReviewApproved),
		q.Deadline.Gt(now),
	)
	total, err := dao.Count()
	if err != nil {
		return nil, 0, err
	}
	values, err := dao.Order(q.Deadline, q.ID.Desc()).Offset((page - 1) * size).Limit(size).Find()
	for _, value := range values {
		rows = append(rows, *value)
	}
	return rows, total, err
}

// ListMine returns tasks related to the supplied user.
func (s *Store) ListMine(ctx context.Context, user uint64, page, size int) ([]domain.Task, int64, error) {
	var rows []domain.Task
	base := idempotency.DB(ctx, s.db).Model(&domain.Task{}).Where("requester_id = ? OR runner_id = ?", user, user)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := base.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error
	return rows, total, err
}

// ListAdmin returns tasks matching moderation filters.
func (s *Store) ListAdmin(
	ctx context.Context,
	search domain.AdminSearch,
	page,
	size int,
) ([]domain.Task, int64, error) {
	db := idempotency.DB(ctx, s.db)
	if keyword := strings.TrimSpace(search.Keyword); keyword != "" {
		pattern := "%" + keyword + "%"
		// GORM Gen cannot group this OR expression with the generated filters.
		db = db.Where(
			"(title LIKE ? OR description LIKE ? OR pickup_location LIKE ? OR dropoff_location LIKE ?)",
			pattern,
			pattern,
			pattern,
			pattern,
		)
	}
	q := platformquery.Use(db).Task
	dao := q.WithContext(ctx)
	if status := strings.TrimSpace(search.Status); status != "" {
		dao = dao.Where(q.Status.Eq(status))
	}
	if reviewStatus := strings.TrimSpace(search.ReviewStatus); reviewStatus != "" {
		dao = dao.Where(q.ReviewStatus.Eq(reviewStatus))
	}
	total, err := dao.Count()
	if err != nil {
		return nil, 0, err
	}
	values, err := dao.Order(q.ID.Desc()).Offset((page - 1) * size).Limit(size).Find()
	if err != nil {
		return nil, 0, err
	}
	rows := make([]domain.Task, 0, len(values))
	for _, value := range values {
		rows = append(rows, *value)
	}
	return rows, total, nil
}

// Update changes an editable open task and requires it to be reviewed again.
func (s *Store) Update(ctx context.Context, id, requester, version uint64, input domain.TaskInput, _ time.Time) (*domain.Task, error) {
	return s.mutate(ctx, id, requester, version, domain.TaskOpen, func(tx *gorm.DB, task *domain.Task) error {
		task.Title, task.Description, task.RewardCents = strings.TrimSpace(input.Title), strings.TrimSpace(input.Description), input.RewardCents
		task.PickupLocation, task.DropoffLocation, task.Deadline, task.Version = strings.TrimSpace(input.PickupLocation), strings.TrimSpace(input.DropoffLocation), input.Deadline.UTC(), task.Version+1
		if input.Contact.Provided {
			ciphertext, err := s.encryptContact(input.Contact.Value, taskContactAAD(task.ID))
			if err != nil {
				return err
			}
			task.ContactType, task.ContactCiphertext = strings.TrimSpace(input.Contact.Type), ciphertext
		}
		task.ReviewStatus = domain.ReviewDraft
		task.ReviewReason = nil
		task.ReviewedBy = nil
		task.ReviewedAt = nil
		if err := tx.Save(task).Error; err != nil {
			return err
		}
		return taskEvent(tx, task, "errand.updated", fmt.Sprintf("errand.updated:%d:%d", task.ID, task.Version))
	})
}

// SubmitReview moves an edited task back into moderation.
func (s *Store) SubmitReview(ctx context.Context, id, requester, version uint64) (*domain.Task, error) {
	return s.mutate(ctx, id, requester, version, domain.TaskOpen, func(tx *gorm.DB, task *domain.Task) error {
		if task.ReviewStatus != domain.ReviewDraft && task.ReviewStatus != domain.ReviewRejected {
			return apperror.New(409, "invalid_errand_review_state", "当前任务不可重新提交审核")
		}
		task.ReviewStatus = domain.ReviewPending
		task.ReviewReason = nil
		task.ReviewedBy = nil
		task.ReviewedAt = nil
		task.Version++
		if err := tx.Save(task).Error; err != nil {
			return err
		}
		return taskEvent(
			tx,
			task,
			"errand.review_submitted",
			fmt.Sprintf("errand.review_submitted:%d:%d", task.ID, task.Version),
		)
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
) (*domain.Task, error) {
	return s.mutate(ctx, id, 0, version, domain.TaskOpen, func(tx *gorm.DB, task *domain.Task) error {
		if task.ReviewStatus != domain.ReviewPending {
			return apperror.New(409, "invalid_errand_review_state", "任务当前不在待审核状态")
		}
		trimmedReason := strings.TrimSpace(reason)
		if !approved && trimmedReason == "" {
			return apperror.New(400, "rejection_reason_required", "驳回原因不能为空")
		}
		task.ReviewStatus = domain.ReviewApproved
		task.ReviewReason = nil
		if !approved {
			task.ReviewStatus = domain.ReviewRejected
			task.ReviewReason = &trimmedReason
		}
		task.ReviewedBy = &adminID
		task.ReviewedAt = &now
		task.Version++
		if err := tx.Save(task).Error; err != nil {
			return err
		}
		return taskEvent(
			tx,
			task,
			"errand.reviewed",
			fmt.Sprintf("errand.reviewed:%d:%d", task.ID, task.Version),
		)
	})
}

// RevokeReview hides an approved, unaccepted task and returns it to moderation.
func (s *Store) RevokeReview(
	ctx context.Context,
	id,
	adminID,
	version uint64,
	reason string,
	now time.Time,
) (*domain.Task, error) {
	return s.mutate(ctx, id, 0, version, domain.TaskOpen, func(tx *gorm.DB, task *domain.Task) error {
		trimmedReason := strings.TrimSpace(reason)
		if trimmedReason == "" {
			return apperror.New(400, "revoke_reason_required", "撤销原因不能为空")
		}
		if task.ReviewStatus != domain.ReviewApproved {
			return apperror.New(409, "invalid_errand_review_state", "仅可撤销已通过的审核结果")
		}
		task.ReviewStatus = domain.ReviewPending
		task.ReviewReason = &trimmedReason
		task.ReviewedBy = &adminID
		task.ReviewedAt = &now
		task.Version++
		if err := tx.Save(task).Error; err != nil {
			return err
		}
		return taskEvent(
			tx,
			task,
			"errand.review_revoked",
			fmt.Sprintf("errand.review_revoked:%d:%d", task.ID, task.Version),
		)
	})
}

// Contact returns either plaintext for an active task participant or a masked value.
func (s *Store) Contact(_ context.Context, task *domain.Task, viewerID uint64) (domain.ContactDetails, error) {
	value, err := s.decryptContact(task.ContactCiphertext, taskContactAAD(task.ID))
	if err != nil {
		return domain.ContactDetails{}, err
	}
	if value == "" {
		return domain.ContactDetails{Type: task.ContactType}, nil
	}
	if task.Status != domain.TaskCompleted && task.Status != domain.TaskCancelled {
		isRequester := viewerID == task.RequesterId
		isActiveRunner := task.RunnerId != nil && viewerID == *task.RunnerId && (task.Status == domain.TaskAccepted || task.Status == domain.TaskPickedUp || task.Status == domain.TaskDelivered)
		if isRequester || isActiveRunner {
			return domain.ContactDetails{Type: task.ContactType, Value: value}, nil
		}
	}
	return domain.ContactDetails{Type: task.ContactType, Value: privacy.MaskContact(value)}, nil
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

func taskContactAAD(id uint64) string { return fmt.Sprintf("errand-task:%d", id) }

// Accept atomically accepts a task and creates its trade order.
func (s *Store) Accept(ctx context.Context, id, runner, version uint64, key string, now time.Time) (*domain.Task, *tradedomain.Order, error) {
	if key = strings.TrimSpace(key); key == "" || len(key) > 128 {
		return nil, nil, apperror.New(400, "invalid_idempotency_key", "Idempotency-Key 无效")
	}
	var task domain.Task
	var order tradedomain.Order
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&task, id).Error; err != nil {
			return taskNotFound(err)
		}
		orderKey := fmt.Sprintf("errand:%d:%s", task.ID, key)
		if err := tx.Where("buyer_id = ? AND idempotency_key = ?", task.RequesterId, orderKey).First(&order).Error; err == nil {
			if order.ResourceType != tradedomain.ResourceErrandTask || order.ResourceId != task.ID || order.SellerId != runner || task.TradeOrderId == nil || *task.TradeOrderId != order.ID {
				return apperror.New(409, "idempotency_key_reused", "幂等键已用于其他请求")
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if task.Version != version {
			return apperror.New(409, "version_conflict", "任务已被其他请求更新")
		}
		if task.Status != domain.TaskOpen {
			return apperror.New(409, "errand_unavailable", "任务当前不可接单")
		}
		if task.ReviewStatus != domain.ReviewApproved {
			return apperror.New(409, "errand_unavailable", "任务尚未通过审核")
		}
		if !task.Deadline.After(now) {
			return apperror.New(409, "errand_expired", "任务已超过接单截止时间")
		}
		if task.RequesterId == runner {
			return apperror.New(403, "self_accept", "不能接自己的跑腿任务")
		}
		snapshot, err := json.Marshal(map[string]any{"task_id": task.ID, "title": task.Title, "pickup_location": task.PickupLocation, "dropoff_location": task.DropoffLocation, "deadline": task.Deadline})
		if err != nil {
			return err
		}
		no, err := newOrderNo()
		if err != nil {
			return err
		}
		order = tradedomain.Order{OrderNo: no, OrderType: tradedomain.OrderTypeErrand, ResourceType: tradedomain.ResourceErrandTask, ResourceId: task.ID, BuyerId: task.RequesterId, SellerId: runner, AmountCents: task.RewardCents, Currency: task.Currency, PaymentMode: tradedomain.PaymentOffline, TradeStatus: tradedomain.StatusConfirmed, FulfillmentStatus: tradedomain.FulfillmentInProgress, TitleSnapshot: task.Title, ResourceSnapshot: snapshot, IdempotencyKey: orderKey, Version: 1}
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
		task.Status, task.RunnerId, task.TradeOrderId, task.AcceptedAt, task.Version = domain.TaskAccepted, &runner, &order.ID, &now, task.Version+1
		if err := tx.Save(&task).Error; err != nil {
			return err
		}
		if err := createOrderTransition(tx, &order, "", tradedomain.StatusConfirmed, runner, "created", "order.created:"+orderKey); err != nil {
			return err
		}
		if err := createTaskTransition(tx, &task, domain.TaskOpen, domain.TaskAccepted, runner, "accepted", "errand.accepted:"+orderKey); err != nil {
			return err
		}
		if err := orderEvent(tx, &order, "order.created", "order.created:"+orderKey); err != nil {
			return err
		}
		return taskEvent(tx, &task, "errand.accepted", fmt.Sprintf("errand.accepted:%d:%d", task.ID, task.Version))
	})
	return &task, &order, err
}

// Pickup marks a task as picked up by its runner.
func (s *Store) Pickup(ctx context.Context, id, runner, version uint64, now time.Time) (*domain.Task, error) {
	return s.runnerTransition(ctx, id, runner, version, domain.TaskAccepted, domain.TaskPickedUp, now, "picked_up")
}

// Deliver marks a task as delivered by its runner.
func (s *Store) Deliver(ctx context.Context, id, runner, version uint64, now time.Time) (*domain.Task, error) {
	return s.runnerTransition(ctx, id, runner, version, domain.TaskPickedUp, domain.TaskDelivered, now, "delivered")
}

func (s *Store) runnerTransition(ctx context.Context, id, runner, version uint64, from, to string, now time.Time, event string) (*domain.Task, error) {
	return s.mutate(ctx, id, 0, version, from, func(tx *gorm.DB, task *domain.Task) error {
		if task.RunnerId == nil || *task.RunnerId != runner {
			return apperror.New(403, "not_runner", "仅接单跑腿员可以执行此操作")
		}
		task.Status, task.Version = to, task.Version+1
		if to == domain.TaskPickedUp {
			task.PickedUpAt = &now
		} else {
			task.DeliveredAt = &now
		}
		if err := tx.Save(task).Error; err != nil {
			return err
		}
		if err := createTaskTransition(tx, task, from, to, runner, event, fmt.Sprintf("errand.%s:%d:%d", event, task.ID, task.Version)); err != nil {
			return err
		}
		return taskEvent(tx, task, "errand."+event, fmt.Sprintf("errand.%s:%d:%d", event, task.ID, task.Version))
	})
}

// Complete marks a delivered task completed and finalizes its order.
func (s *Store) Complete(ctx context.Context, id, requester, version uint64, now time.Time) (*domain.Task, *tradedomain.Order, error) {
	task, order, err := s.finish(ctx, id, requester, version, domain.TaskCompleted, now)
	return task, order, err
}

// Cancel cancels a task and, when present, its trade order.
func (s *Store) Cancel(ctx context.Context, id, actor, version uint64, now time.Time) (*domain.Task, *tradedomain.Order, error) {
	task, order, err := s.finish(ctx, id, actor, version, domain.TaskCancelled, now)
	return task, order, err
}

// CompleteOrder completes the order associated with a task.
func (s *Store) CompleteOrder(ctx context.Context, orderID, actor, version uint64, now time.Time) (*tradedomain.Order, error) {
	var task domain.Task
	if err := idempotency.DB(ctx, s.db).Where("trade_order_id = ?", orderID).First(&task).Error; err != nil {
		return nil, taskNotFound(err)
	}
	var order tradedomain.Order
	if err := idempotency.DB(ctx, s.db).First(&order, orderID).Error; err != nil {
		return nil, err
	}
	if order.Version != version {
		return nil, apperror.New(409, "version_conflict", "订单已被其他请求更新")
	}
	_, completed, err := s.Complete(ctx, task.ID, actor, task.Version, now)
	return completed, err
}

// CancelOrder cancels the order associated with a task.
func (s *Store) CancelOrder(ctx context.Context, orderID, actor, version uint64, now time.Time) (*tradedomain.Order, error) {
	var task domain.Task
	if err := idempotency.DB(ctx, s.db).Where("trade_order_id = ?", orderID).First(&task).Error; err != nil {
		return nil, taskNotFound(err)
	}
	var order tradedomain.Order
	if err := idempotency.DB(ctx, s.db).First(&order, orderID).Error; err != nil {
		return nil, err
	}
	if order.Version != version {
		return nil, apperror.New(409, "version_conflict", "订单已被其他请求更新")
	}
	_, cancelled, err := s.Cancel(ctx, task.ID, actor, task.Version, now)
	return cancelled, err
}
func (s *Store) finish(ctx context.Context, id, actor, version uint64, target string, now time.Time) (*domain.Task, *tradedomain.Order, error) {
	var task domain.Task
	var order tradedomain.Order
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&task, id).Error; err != nil {
			return taskNotFound(err)
		}
		if task.Version != version {
			return apperror.New(409, "version_conflict", "任务已被其他请求更新")
		}
		if target == domain.TaskCompleted {
			if task.Status != domain.TaskDelivered {
				return apperror.New(409, "invalid_errand_state", "任务尚未送达")
			}
			if task.RequesterId != actor {
				return apperror.New(403, "not_requester", "仅发布者可以确认完成")
			}
		} else {
			if task.Status != domain.TaskOpen && task.Status != domain.TaskAccepted {
				return apperror.New(409, "invalid_errand_state", "当前任务不允许取消")
			}
			if task.RequesterId != actor && (task.RunnerId == nil || *task.RunnerId != actor) {
				return apperror.New(403, "not_task_party", "仅任务双方可以取消")
			}
		}
		from := task.Status
		task.Status, task.Version = target, task.Version+1
		if target == domain.TaskCompleted {
			task.CompletedAt = &now
		} else {
			task.CancelledAt = &now
		}
		if err := tx.Save(&task).Error; err != nil {
			return err
		}
		if task.TradeOrderId != nil {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, *task.TradeOrderId).Error; err != nil {
				return err
			}
			orderFrom := order.TradeStatus
			order.TradeStatus, order.Version = map[bool]string{true: tradedomain.StatusCompleted, false: tradedomain.StatusCancelled}[target == domain.TaskCompleted], order.Version+1
			if target == domain.TaskCompleted {
				order.CompletedAt = &now
				order.FulfillmentStatus = tradedomain.FulfillmentDelivered
			} else {
				order.CancelledAt = &now
			}
			if err := tx.Save(&order).Error; err != nil {
				return err
			}
			if err := createOrderTransition(tx, &order, orderFrom, order.TradeStatus, actor, target, fmt.Sprintf("order.%s:%d:%d", order.TradeStatus, order.ID, order.Version)); err != nil {
				return err
			}
			if err := orderEvent(tx, &order, "order."+order.TradeStatus, fmt.Sprintf("order.%s:%d:%d", order.TradeStatus, order.ID, order.Version)); err != nil {
				return err
			}
		}
		if err := createTaskTransition(tx, &task, from, target, actor, target, fmt.Sprintf("errand.%s:%d:%d", target, task.ID, task.Version)); err != nil {
			return err
		}
		return taskEvent(tx, &task, "errand."+target, fmt.Sprintf("errand.%s:%d:%d", target, task.ID, task.Version))
	})
	return &task, &order, err
}
func (s *Store) mutate(ctx context.Context, id, requester, version uint64, status string, fn func(*gorm.DB, *domain.Task) error) (*domain.Task, error) {
	var task domain.Task
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&task, id).Error; err != nil {
			return taskNotFound(err)
		}
		if requester != 0 && task.RequesterId != requester {
			return apperror.New(403, "not_requester", "仅发布者可以执行此操作")
		}
		if task.Version != version {
			return apperror.New(409, "version_conflict", "任务已被其他请求更新")
		}
		if task.Status != status {
			return apperror.New(409, "invalid_errand_state", "当前任务状态不允许此操作")
		}
		return fn(tx, &task)
	})
	return &task, err
}
func createTaskTransition(tx *gorm.DB, task *domain.Task, from, to string, actor uint64, reason, key string) error {
	var id *uint64
	if actor != 0 {
		id = &actor
	}
	return tx.Create(&domain.Transition{TaskId: task.ID, FromStatus: from, ToStatus: to, ActorId: id, Reason: &reason, IdempotencyKey: key}).Error
}
func createOrderTransition(tx *gorm.DB, order *tradedomain.Order, from, to string, actor uint64, reason, key string) error {
	var id *uint64
	if actor != 0 {
		id = &actor
	}
	return tx.Create(&tradedomain.OrderTransition{OrderId: order.ID, FromStatus: from, ToStatus: to, ActorType: "user", ActorId: id, ReasonCode: &reason, IdempotencyKey: key}).Error
}
func taskEvent(tx *gorm.DB, task *domain.Task, kind, key string) error {
	return domainevent.WriteWithKey(tx, "errand", task.ID, kind, key, map[string]any{"task_id": task.ID, "status": task.Status, "version": task.Version})
}
func orderEvent(tx *gorm.DB, order *tradedomain.Order, kind, key string) error {
	return domainevent.WriteWithKey(tx, "order", order.ID, kind, key, order)
}
func taskNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperror.New(404, "errand_not_found", "跑腿任务不存在")
	}
	return err
}
func newOrderNo() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("TRD%x", b), nil
}
