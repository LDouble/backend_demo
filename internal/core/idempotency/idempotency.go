// Package idempotency provides permanent, transaction-scoped HTTP write deduplication.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// MaxKeyLength is the public Idempotency-Key contract.
	MaxKeyLength = 128
	// MaxResponseBytes bounds the permanent response snapshot.
	MaxResponseBytes = 64 << 10

	// StatusPending means the owning business transaction has not completed.
	StatusPending = "pending"
	// StatusCompleted means the response is permanently replayable.
	StatusCompleted = "completed"
)

// Record is a permanent first-result snapshot keyed by actor, operation and key hash.
type Record struct {
	ID              uint64 `gorm:"primaryKey;autoIncrement"`
	ActorID         uint64 `gorm:"not null;uniqueIndex:uk_idempotency_scope,priority:1"`
	OperationID     string `gorm:"size:128;not null;uniqueIndex:uk_idempotency_scope,priority:2"`
	KeyHash         string `gorm:"size:64;not null;uniqueIndex:uk_idempotency_scope,priority:3"`
	RequestHash     string `gorm:"size:64;not null"`
	Status          string `gorm:"size:16;not null;index"`
	HTTPStatus      int    `gorm:"not null"`
	ResponseBody    []byte `gorm:"type:blob"`
	ResponseHeaders []byte `gorm:"type:json"`
	ResourceType    string `gorm:"size:64"`
	ResourceID      *uint64
	CompletedAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// TableName returns the shared platform table.
func (Record) TableName() string { return "idempotency_records" }

// Request identifies one authenticated write attempt.
type Request struct {
	ActorID     uint64
	OperationID string
	Key         string
	RequestHash string
}

// Result is the stable HTTP outcome persisted for replay.
type Result struct {
	HTTPStatus   int
	Body         []byte
	Headers      http.Header
	ResourceType string
	ResourceID   *uint64
	Replayed     bool
}

type transactionContextKey struct{}

// WithTransaction attaches the owning idempotency transaction to a request context.
func WithTransaction(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, transactionContextKey{}, tx)
}

// DB returns the request transaction when present, otherwise the fallback database.
func DB(ctx context.Context, fallback *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(transactionContextKey{}).(*gorm.DB); ok && tx != nil {
		return tx.WithContext(ctx)
	}
	return fallback.WithContext(ctx)
}

// InTransaction reports whether the request is executing inside an idempotency transaction.
func InTransaction(ctx context.Context) bool {
	tx, ok := ctx.Value(transactionContextKey{}).(*gorm.DB)
	return ok && tx != nil
}

// Execute reserves, performs and completes one write in the same DB transaction.
func Execute(
	ctx context.Context,
	db *gorm.DB,
	request Request,
	work func(*gorm.DB) (Result, error),
) (Result, error) {
	if err := validateRequest(request); err != nil {
		return Result{}, err
	}
	var result Result
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, replay, err := reserve(tx, request)
		if err != nil {
			return err
		}
		if replay {
			result = Result{
				HTTPStatus: record.HTTPStatus, Body: append([]byte(nil), record.ResponseBody...),
				ResourceType: record.ResourceType, ResourceID: record.ResourceID, Replayed: true,
			}
			if len(record.ResponseHeaders) > 0 {
				if err := json.Unmarshal(record.ResponseHeaders, &result.Headers); err != nil {
					return fmt.Errorf("decode idempotency response headers: %w", err)
				}
			}
			return nil
		}
		result, err = work(tx)
		if err != nil {
			return err
		}
		if result.HTTPStatus < 200 || result.HTTPStatus > 599 {
			return fmt.Errorf("idempotency result has invalid HTTP status %d", result.HTTPStatus)
		}
		if len(result.Body) > MaxResponseBytes {
			return apperror.New(http.StatusInternalServerError, "idempotency_response_too_large", "幂等响应快照超过限制")
		}
		result.Headers = cloneHeaders(result.Headers)
		headers, err := json.Marshal(result.Headers)
		if err != nil {
			return fmt.Errorf("encode idempotency response headers: %w", err)
		}
		now := time.Now().UTC()
		update := map[string]any{
			"status": StatusCompleted, "http_status": result.HTTPStatus,
			"response_body": result.Body, "resource_type": result.ResourceType,
			"response_headers": headers, "resource_id": result.ResourceID, "completed_at": now,
		}
		changed := tx.Model(&Record{}).
			Where("id = ? AND status = ?", record.ID, StatusPending).
			Updates(update)
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return fmt.Errorf("idempotency record %d lost pending ownership", record.ID)
		}
		return nil
	})
	return result, err
}

// RequestHash returns a canonical SHA-256 hash for JSON-compatible input.
func RequestHash(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode idempotency request: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func reserve(tx *gorm.DB, request Request) (*Record, bool, error) {
	keyHash := hashString(strings.TrimSpace(request.Key))
	var existing Record
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("actor_id = ? AND operation_id = ? AND key_hash = ?", request.ActorID, request.OperationID, keyHash).
		Take(&existing).Error
	if err == nil {
		return replay(&existing, request)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	record := &Record{
		ActorID: request.ActorID, OperationID: request.OperationID, KeyHash: keyHash,
		RequestHash: request.RequestHash, Status: StatusPending,
	}
	if err = tx.Create(record).Error; err != nil {
		if lookupErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("actor_id = ? AND operation_id = ? AND key_hash = ?", request.ActorID, request.OperationID, keyHash).
			Take(&existing).Error; lookupErr != nil {
			return nil, false, fmt.Errorf("reserve idempotency record: %w", err)
		}
		return replay(&existing, request)
	}
	return record, false, nil
}

func replay(record *Record, request Request) (*Record, bool, error) {
	if !bytes.Equal([]byte(record.RequestHash), []byte(request.RequestHash)) {
		return nil, false, apperror.New(http.StatusConflict, "idempotency_key_reused", "幂等键已用于不同请求")
	}
	if record.Status != StatusCompleted {
		return nil, false, apperror.New(http.StatusConflict, "idempotency_in_progress", "相同请求正在处理中")
	}
	return record, true, nil
}

func validateRequest(request Request) error {
	request.Key = strings.TrimSpace(request.Key)
	if request.ActorID == 0 || strings.TrimSpace(request.OperationID) == "" {
		return fmt.Errorf("idempotency actor and operation are required")
	}
	if request.Key == "" || len(request.Key) > MaxKeyLength {
		return apperror.New(http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key 缺失或超过 128 字符")
	}
	if len(request.RequestHash) != sha256.Size*2 {
		return fmt.Errorf("idempotency request hash must be SHA-256")
	}
	return nil
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func cloneHeaders(source http.Header) http.Header {
	result := make(http.Header)
	for name, values := range source {
		for _, value := range values {
			result.Add(name, value)
		}
	}
	return result
}
