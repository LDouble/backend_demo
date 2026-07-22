package redisclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var fixedWindowScript = redis.NewScript(`
local current = redis.call('INCR', KEYS[1])
if current == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return current`)

// AuthLimiter applies distributed fixed-window limits for authentication endpoints.
type AuthLimiter struct {
	client     *redis.Client
	failClosed bool
}

// NewAuthLimiter creates a Redis-backed limiter. Production should fail closed.
func NewAuthLimiter(client *redis.Client, failClosed bool) *AuthLimiter {
	return &AuthLimiter{client: client, failClosed: failClosed}
}

// AllowLoginIP limits pre-authentication password hashing by source IP.
func (l *AuthLimiter) AllowLoginIP(ctx context.Context, ip string) (bool, error) {
	return l.allow(ctx, []limitScope{{
		key: "auth:login:ip:" + limiterHash(ip), limit: 30, window: 15 * time.Minute,
	}})
}

// AllowWeChatLogin limits WeChat Mini Program login traffic by source IP.
//
// The shared AllowLoginIP scope counts every attempt (success or failure)
// against 30 requests per 15 minutes, which is too tight for shared egress
// (campus NAT, carrier-grade NAT) where many distinct end users share one
// public IP. WeChat logins are additionally gated upstream by the
// authoritative jscode2session, so the local limiter is a coarse
// anti-abuse signal rather than a brute-force defense; a much larger window
// and per-attempt ceiling keeps a single NAT pool from locking out valid
// users while still shedding obviously broken callers.
func (l *AuthLimiter) AllowWeChatLogin(ctx context.Context, ip string) (bool, error) {
	return l.allow(ctx, []limitScope{{
		key: "auth:wechat:ip:" + limiterHash(ip), limit: 600, window: 15 * time.Minute,
	}})
}

// RecordLoginFailure tracks a username only after constant-time credential
// verification. It never prevents a later correct password from being checked.
func (l *AuthLimiter) RecordLoginFailure(ctx context.Context, username string) (bool, error) {
	return l.allow(ctx, []limitScope{{
		key: loginFailureKey(username), limit: 5, window: 15 * time.Minute,
	}})
}

// ClearLoginFailures clears the post-verification username failure counter.
func (l *AuthLimiter) ClearLoginFailures(ctx context.Context, username string) error {
	if err := l.client.Del(ctx, loginFailureKey(username)).Err(); err != nil && l.failClosed {
		return fmt.Errorf("clear authentication failure limiter: %w", err)
	}
	return nil
}

// AllowRefresh enforces refresh-family and source-IP limits.
func (l *AuthLimiter) AllowRefresh(ctx context.Context, token, ip string) (bool, error) {
	return l.allow(ctx, []limitScope{
		{key: "auth:refresh:family:" + limiterHash(token), limit: 20, window: 5 * time.Minute},
		{key: "auth:refresh:ip:" + limiterHash(ip), limit: 20, window: 5 * time.Minute},
	})
}

type limitScope struct {
	key    string
	limit  int64
	window time.Duration
}

func (l *AuthLimiter) allow(ctx context.Context, scopes []limitScope) (bool, error) {
	allowed := true
	for _, scope := range scopes {
		count, err := fixedWindowScript.Run(
			ctx,
			l.client,
			[]string{scope.key},
			strconv.FormatInt(scope.window.Milliseconds(), 10),
		).Int64()
		if err != nil {
			if l.failClosed {
				return false, fmt.Errorf("authentication rate limiter unavailable: %w", err)
			}
			return true, nil
		}
		if count > scope.limit {
			allowed = false
		}
	}
	return allowed, nil
}

func limiterHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func loginFailureKey(username string) string {
	return "auth:login:user:" + limiterHash(strings.ToLower(strings.TrimSpace(username)))
}
