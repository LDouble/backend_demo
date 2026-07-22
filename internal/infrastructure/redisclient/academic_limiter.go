package redisclient

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const academicFailureLimit int64 = 5

var recordAcademicFailureScript = redis.NewScript(`
for _, key in ipairs(KEYS) do
  local current = redis.call('INCR', key)
  if current == 1 then redis.call('PEXPIRE', key, ARGV[1]) end
end
return 1`)

// AcademicLimiter tracks only failed provider verifications across three privacy-safe scopes.
type AcademicLimiter struct {
	client     *redis.Client
	failClosed bool
}

// NewAcademicLimiter creates a distributed academic credential failure limiter.
func NewAcademicLimiter(client *redis.Client, failClosed bool) *AcademicLimiter {
	return &AcademicLimiter{client: client, failClosed: failClosed}
}

// Allow checks existing failure counters without charging successful attempts.
func (l *AcademicLimiter) Allow(ctx context.Context, userID uint64, studentNo, ip string) (bool, error) {
	keys := academicFailureKeys(userID, studentNo, ip)
	values, err := l.client.MGet(ctx, keys...).Result()
	if err != nil {
		if l.failClosed {
			return false, fmt.Errorf("academic rate limiter unavailable: %w", err)
		}
		return true, nil
	}
	for _, value := range values {
		if value == nil {
			continue
		}
		count, parseErr := strconv.ParseInt(fmt.Sprint(value), 10, 64)
		if parseErr != nil {
			if l.failClosed {
				return false, fmt.Errorf("decode academic rate limit counter: %w", parseErr)
			}
			continue
		}
		if count >= academicFailureLimit {
			return false, nil
		}
	}
	return true, nil
}

// RecordFailure increments all failure scopes with a 15-minute lifetime.
func (l *AcademicLimiter) RecordFailure(ctx context.Context, userID uint64, studentNo, ip string) error {
	keys := academicFailureKeys(userID, studentNo, ip)
	err := recordAcademicFailureScript.Run(
		ctx,
		l.client,
		keys,
		strconv.FormatInt((15*time.Minute).Milliseconds(), 10),
	).Err()
	if err != nil && l.failClosed {
		return fmt.Errorf("record academic rate limit failure: %w", err)
	}
	return nil
}

// Clear removes all scopes after a successful verification.
func (l *AcademicLimiter) Clear(ctx context.Context, userID uint64, studentNo, ip string) error {
	if err := l.client.Del(ctx, academicFailureKeys(userID, studentNo, ip)...).Err(); err != nil && l.failClosed {
		return fmt.Errorf("clear academic rate limit failures: %w", err)
	}
	return nil
}

func academicFailureKeys(userID uint64, studentNo, ip string) []string {
	return []string{
		"academic:failure:user:" + limiterHash(strconv.FormatUint(userID, 10)),
		"academic:failure:student:" + limiterHash(strings.TrimSpace(studentNo)),
		"academic:failure:ip:" + limiterHash(strings.TrimSpace(ip)),
	}
}
