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

// AllowLogin enforces username and source-IP login limits.
func (l *AuthLimiter) AllowLogin(ctx context.Context, username, ip string) (bool, error) {
	return l.allow(ctx, []limitScope{
		{key: "auth:login:user:" + limiterHash(strings.ToLower(strings.TrimSpace(username))), limit: 5, window: 15 * time.Minute},
		{key: "auth:login:ip:" + limiterHash(ip), limit: 30, window: 15 * time.Minute},
	})
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
