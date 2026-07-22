package redisclient

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestAcademicLimiterCountsOnlyFailuresAndClearsAllScopes(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	limiter := NewAcademicLimiter(client, true)
	ctx := context.Background()
	for attempt := 0; attempt < 5; attempt++ {
		allowed, err := limiter.Allow(ctx, 7, "20260001", "192.0.2.1")
		if err != nil || !allowed {
			t.Fatalf("attempt=%d allowed=%v err=%v", attempt+1, allowed, err)
		}
		if err = limiter.RecordFailure(ctx, 7, "20260001", "192.0.2.1"); err != nil {
			t.Fatal(err)
		}
	}
	allowed, err := limiter.Allow(ctx, 7, "20260001", "192.0.2.1")
	if err != nil || allowed {
		t.Fatalf("sixth attempt allowed=%v err=%v", allowed, err)
	}
	if err = limiter.Clear(ctx, 7, "20260001", "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	allowed, err = limiter.Allow(ctx, 7, "20260001", "192.0.2.1")
	if err != nil || !allowed {
		t.Fatalf("cleared limiter allowed=%v err=%v", allowed, err)
	}
}

func TestAcademicLimiterSharesStudentAndIPScopes(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	limiter := NewAcademicLimiter(client, true)
	ctx := context.Background()
	for range 5 {
		if err := limiter.RecordFailure(ctx, 1, "shared", "192.0.2.8"); err != nil {
			t.Fatal(err)
		}
	}
	if allowed, err := limiter.Allow(ctx, 2, "shared", "198.51.100.9"); err != nil || allowed {
		t.Fatalf("shared student scope allowed=%v err=%v", allowed, err)
	}
	if allowed, err := limiter.Allow(ctx, 2, "different", "192.0.2.8"); err != nil || allowed {
		t.Fatalf("shared IP scope allowed=%v err=%v", allowed, err)
	}
}
