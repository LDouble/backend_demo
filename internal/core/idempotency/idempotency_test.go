package idempotency

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"gorm.io/gorm"
)

func TestExecuteReplayAndConflict(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&Record{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	hash, err := RequestHash(map[string]any{"listing_id": 7})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{ActorID: 9, OperationID: "CreateMarketplaceOrder", Key: "stable-key", RequestHash: hash}
	var calls atomic.Int64
	work := func(*gorm.DB) (Result, error) {
		calls.Add(1)
		id := uint64(42)
		return Result{
			HTTPStatus:   http.StatusCreated,
			Body:         []byte(`{"id":42}`),
			Headers:      http.Header{"Content-Type": {"application/json"}, "ETag": {`"42"`}},
			ResourceType: "order",
			ResourceID:   &id,
		}, nil
	}
	first, err := Execute(context.Background(), db, request, work)
	if err != nil || first.Replayed {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := Execute(context.Background(), db, request, work)
	if err != nil || !second.Replayed || calls.Load() != 1 {
		t.Fatalf("second=%+v calls=%d err=%v", second, calls.Load(), err)
	}
	if got := second.Headers.Get("Content-Type"); got != "application/json" {
		t.Fatalf("replayed content type=%q", got)
	}
	if got := second.Headers.Get("ETag"); got != `"42"` {
		t.Fatalf("replayed etag=%q", got)
	}
	differentHash, _ := RequestHash(map[string]any{"listing_id": 8})
	request.RequestHash = differentHash
	_, err = Execute(context.Background(), db, request, work)
	var appError *apperror.Error
	if !errors.As(err, &appError) || appError.Status != http.StatusConflict {
		t.Fatalf("conflict error=%v", err)
	}
}

func TestExecuteRollsBackReservationOnBusinessError(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&Record{}); err != nil {
		t.Fatal(err)
	}
	hash, _ := RequestHash("request")
	want := errors.New("business failed")
	_, err = Execute(context.Background(), db, Request{ActorID: 1, OperationID: "Write", Key: "key", RequestHash: hash}, func(*gorm.DB) (Result, error) {
		return Result{}, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error=%v", err)
	}
	var count int64
	if err = db.Model(&Record{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestExecuteValidatesRequestAndResultBounds(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&Record{}); err != nil {
		t.Fatal(err)
	}
	hash, _ := RequestHash("request")
	work := func(*gorm.DB) (Result, error) { return Result{HTTPStatus: 200}, nil }
	for _, request := range []Request{
		{OperationID: "Write", Key: "key", RequestHash: hash},
		{ActorID: 1, Key: "key", RequestHash: hash},
		{ActorID: 1, OperationID: "Write", RequestHash: hash},
		{ActorID: 1, OperationID: "Write", Key: "key", RequestHash: "short"},
	} {
		if _, err = Execute(context.Background(), db, request, work); err == nil {
			t.Fatalf("invalid request accepted: %+v", request)
		}
	}
	request := Request{ActorID: 1, OperationID: "Write", Key: "key", RequestHash: hash}
	if _, err = Execute(context.Background(), db, request, func(*gorm.DB) (Result, error) {
		return Result{HTTPStatus: 0}, nil
	}); err == nil {
		t.Fatal("invalid status accepted")
	}
	request.Key = "large-response"
	if _, err = Execute(context.Background(), db, request, func(*gorm.DB) (Result, error) {
		return Result{HTTPStatus: 200, Body: bytes.Repeat([]byte("x"), MaxResponseBytes+1)}, nil
	}); err == nil {
		t.Fatal("oversized response accepted")
	}
	if _, err = RequestHash(make(chan int)); err == nil {
		t.Fatal("unencodable request accepted")
	}
}

func TestDBUsesRequestTransaction(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { _ = tx.Rollback().Error })
	if got := DB(WithTransaction(context.Background(), tx), db); got.Statement.ConnPool != tx.Statement.ConnPool {
		t.Fatal("request transaction was not propagated")
	}
	if got := DB(context.Background(), db); got.Statement.ConnPool != db.Statement.ConnPool {
		t.Fatal("fallback database was not used")
	}
}

func TestAfterCommitCallbacks(t *testing.T) {
	ctx, callbacks := WithAfterCommit(context.Background())
	var calls atomic.Int64
	if err := DeferAfterCommit(ctx, func(context.Context) error {
		calls.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("callback ran before commit")
	}
	if err := callbacks.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("callback calls=%d", calls.Load())
	}
	if err := callbacks.Run(ctx); err != nil || calls.Load() != 1 {
		t.Fatalf("second run err=%v calls=%d", err, calls.Load())
	}
}

func TestDeferAfterCommitRunsImmediatelyWithoutRegistry(t *testing.T) {
	called := false
	if err := DeferAfterCommit(context.Background(), func(context.Context) error {
		called = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("callback did not run outside a transaction boundary")
	}
}
