package redisclient

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/weouc-plus/campus-platform/internal/core/auth"
)

type sessionStore struct{ client *redis.Client }

// NewSessionStore creates a Redis-backed authentication session store.
func NewSessionStore(client *redis.Client) auth.SessionStore { return &sessionStore{client: client} }
func key(sid string) string                                  { return "session:" + sid }
func userSessionsKey(userID uint64) string                   { return "sessions:user:" + strconv.FormatUint(userID, 10) }
func (s *sessionStore) Create(ctx context.Context, sid string, userID uint64, hash string, ttl time.Duration) error {
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, key(sid), "user_id", strconv.FormatUint(userID, 10), "refresh_hash", hash)
	pipe.Expire(ctx, key(sid), ttl)
	pipe.SAdd(ctx, userSessionsKey(userID), sid)
	pipe.Expire(ctx, userSessionsKey(userID), ttl)
	_, err := pipe.Exec(ctx)
	return err
}
func (s *sessionStore) Exists(ctx context.Context, sid string) (bool, error) {
	n, err := s.client.Exists(ctx, key(sid)).Result()
	return n == 1, err
}

var rotateScript = redis.NewScript(`
if redis.call('HGET', KEYS[1], 'refresh_hash') ~= ARGV[1] then return 0 end
local user_id = redis.call('HGET', KEYS[1], 'user_id')
if not user_id then return 0 end
redis.call('HSET', KEYS[1], 'refresh_hash', ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
local user_sessions = 'sessions:user:' .. user_id
redis.call('SADD', user_sessions, ARGV[4])
redis.call('PEXPIRE', user_sessions, ARGV[3])
return 1`)

func (s *sessionStore) Rotate(ctx context.Context, sid, oldHash, newHash string, ttl time.Duration) (bool, error) {
	n, err := rotateScript.Run(ctx, s.client, []string{key(sid)}, oldHash, newHash, strconv.FormatInt(ttl.Milliseconds(), 10), sid).Int64()
	if err != nil {
		return false, fmt.Errorf("run rotation script: %w", err)
	}
	return n == 1, nil
}
func (s *sessionStore) Delete(ctx context.Context, sid string) error {
	userID, err := s.client.HGet(ctx, key(sid), "user_id").Uint64()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	pipe := s.client.TxPipeline()
	pipe.Del(ctx, key(sid))
	if userID != 0 {
		pipe.SRem(ctx, userSessionsKey(userID), sid)
	}
	_, err = pipe.Exec(ctx)
	return err
}

func (s *sessionStore) DeleteUser(ctx context.Context, userID uint64) error {
	setKey := userSessionsKey(userID)
	sessions, err := s.client.SMembers(ctx, setKey).Result()
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(sessions)+1)
	keys = append(keys, setKey)
	for _, sid := range sessions {
		keys = append(keys, key(sid))
	}
	return s.client.Del(ctx, keys...).Err()
}
