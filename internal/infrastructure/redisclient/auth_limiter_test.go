package redisclient

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestAuthLimiterSeparatesIPAndPostVerificationAccountLimits(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	limiter := NewAuthLimiter(client, true)
	ctx := context.Background()

	for attempt := 0; attempt < 6; attempt++ {
		allowed, err := limiter.RecordLoginFailure(ctx, "Known.User")
		if err != nil {
			t.Fatal(err)
		}
		if got, want := allowed, attempt < 5; got != want {
			t.Fatalf("attempt=%d allowed=%v want=%v", attempt+1, got, want)
		}
	}
	allowed, err := limiter.AllowLoginIP(ctx, "192.0.2.1")
	if err != nil || !allowed {
		t.Fatalf("account failures blocked password verification: allowed=%v err=%v", allowed, err)
	}
	if err = limiter.ClearLoginFailures(ctx, "known.user"); err != nil {
		t.Fatal(err)
	}
	allowed, err = limiter.RecordLoginFailure(ctx, "KNOWN.USER")
	if err != nil || !allowed {
		t.Fatalf("cleared account remained limited: allowed=%v err=%v", allowed, err)
	}
}

// TestAuthLimiterWeChatAndPasswordShareNoKeyspace locks in the Codex P2 fix:
// WeChat Mini Program login and password login must use independent Redis
// keys so a campus NAT or carrier-grade NAT does not exhaust the password
// limit by being the egress for unrelated WeChat users.
func TestAuthLimiterWeChatAndPasswordShareNoKeyspace(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	limiter := NewAuthLimiter(client, true)
	ctx := context.Background()

	for i := 0; i < 30; i++ {
		if _, err := limiter.AllowLoginIP(ctx, "192.0.2.1"); err != nil {
			t.Fatal(err)
		}
	}
	// Password limiter is now saturated.
	if allowed, err := limiter.AllowLoginIP(ctx, "192.0.2.1"); err != nil || allowed {
		t.Fatalf("password limiter did not cap at 30: allowed=%v err=%v", allowed, err)
	}
	// WeChat limiter remains usable for the same IP.
	for i := 0; i < 5; i++ {
		allowed, err := limiter.AllowWeChatLogin(ctx, "192.0.2.1")
		if err != nil || !allowed {
			t.Fatalf("wechat attempt %d unexpectedly limited: allowed=%v err=%v", i+1, allowed, err)
		}
	}
}
