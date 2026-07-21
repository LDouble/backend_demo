package redisclient

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/weouc-plus/campus-platform/internal/core/auth"
)

type sessionStore struct{ client *redis.Client }

const maxActiveSessionsPerUser = 20

// NewSessionStore creates a Redis-backed authentication session store.
func NewSessionStore(client *redis.Client) auth.SessionStore { return &sessionStore{client: client} }
func key(sid string) string                                  { return "session:" + sid }
func userSessionsKey(userID uint64) string {
	return "sessions:v2:user:" + strconv.FormatUint(userID, 10)
}
func legacyUserSessionsKey(userID uint64) string {
	return "sessions:user:" + strconv.FormatUint(userID, 10)
}

var createSessionScript = redis.NewScript(`
local session_key = KEYS[1]
local user_sessions = KEYS[2]
local sid = ARGV[1]
local user_id = ARGV[2]
local refresh_hash = ARGV[3]
local ttl = tonumber(ARGV[4])
local redis_time = redis.call('TIME')
local now = tonumber(redis_time[1]) * 1000 + math.floor(tonumber(redis_time[2]) / 1000)
local expires_at = now + ttl

redis.call('HSET', session_key, 'user_id', user_id, 'refresh_hash', refresh_hash)
redis.call('PEXPIRE', session_key, ttl)

local expired = redis.call('ZRANGEBYSCORE', user_sessions, '-inf', now)
for _, expired_sid in ipairs(expired) do
  redis.call('DEL', 'session:' .. expired_sid)
end
redis.call('ZREMRANGEBYSCORE', user_sessions, '-inf', now)
redis.call('ZADD', user_sessions, expires_at, sid)

local excess = redis.call('ZCARD', user_sessions) - tonumber(ARGV[5])
if excess > 0 then
  local oldest = redis.call('ZRANGE', user_sessions, 0, excess - 1)
  for _, old_sid in ipairs(oldest) do
    redis.call('DEL', 'session:' .. old_sid)
    redis.call('ZREM', user_sessions, old_sid)
  end
end

local newest = redis.call('ZRANGE', user_sessions, -1, -1, 'WITHSCORES')
if newest[2] then redis.call('PEXPIREAT', user_sessions, newest[2]) end
return 1`)

func (s *sessionStore) Create(ctx context.Context, sid string, userID uint64, hash string, ttl time.Duration) error {
	return createSessionScript.Run(ctx, s.client, []string{key(sid), userSessionsKey(userID)},
		sid,
		strconv.FormatUint(userID, 10),
		hash,
		strconv.FormatInt(ttl.Milliseconds(), 10),
		strconv.Itoa(maxActiveSessionsPerUser),
	).Err()
}
func (s *sessionStore) Exists(ctx context.Context, sid string) (bool, error) {
	n, err := s.client.Exists(ctx, key(sid)).Result()
	return n == 1, err
}

var rotateScript = redis.NewScript(`
if redis.call('HGET', KEYS[1], 'refresh_hash') ~= ARGV[1] then return 0 end
local user_id = redis.call('HGET', KEYS[1], 'user_id')
if not user_id then return 0 end
local ttl = tonumber(ARGV[3])
local redis_time = redis.call('TIME')
local now = tonumber(redis_time[1]) * 1000 + math.floor(tonumber(redis_time[2]) / 1000)
local expires_at = now + ttl
redis.call('HSET', KEYS[1], 'refresh_hash', ARGV[2])
redis.call('PEXPIRE', KEYS[1], ttl)
local user_sessions = 'sessions:v2:user:' .. user_id
local expired = redis.call('ZRANGEBYSCORE', user_sessions, '-inf', now)
for _, expired_sid in ipairs(expired) do
  redis.call('DEL', 'session:' .. expired_sid)
end
redis.call('ZREMRANGEBYSCORE', user_sessions, '-inf', now)
redis.call('ZADD', user_sessions, expires_at, ARGV[4])
local excess = redis.call('ZCARD', user_sessions) - tonumber(ARGV[5])
if excess > 0 then
  local oldest = redis.call('ZRANGE', user_sessions, 0, excess - 1)
  for _, old_sid in ipairs(oldest) do
    redis.call('DEL', 'session:' .. old_sid)
    redis.call('ZREM', user_sessions, old_sid)
  end
end
local newest = redis.call('ZRANGE', user_sessions, -1, -1, 'WITHSCORES')
if newest[2] then redis.call('PEXPIREAT', user_sessions, newest[2]) end
return 1`)

func (s *sessionStore) Rotate(ctx context.Context, sid, oldHash, newHash string, ttl time.Duration) (bool, error) {
	n, err := rotateScript.Run(ctx, s.client, []string{key(sid)},
		oldHash,
		newHash,
		strconv.FormatInt(ttl.Milliseconds(), 10),
		sid,
		strconv.Itoa(maxActiveSessionsPerUser),
	).Int64()
	if err != nil {
		return false, fmt.Errorf("run rotation script: %w", err)
	}
	return n == 1, nil
}

var deleteSessionScript = redis.NewScript(`
local user_id = redis.call('HGET', KEYS[1], 'user_id')
redis.call('DEL', KEYS[1])
if user_id then
  redis.call('ZREM', 'sessions:v2:user:' .. user_id, ARGV[1])
  redis.call('SREM', 'sessions:user:' .. user_id, ARGV[1])
end
return 1`)

func (s *sessionStore) Delete(ctx context.Context, sid string) error {
	return deleteSessionScript.Run(ctx, s.client, []string{key(sid)}, sid).Err()
}

var deleteUserSessionsScript = redis.NewScript(`
local sessions = redis.call('ZRANGE', KEYS[1], 0, -1)
local legacy_sessions = redis.call('SMEMBERS', KEYS[2])
local seen = {}
for _, sid in ipairs(sessions) do
  seen[sid] = true
  redis.call('DEL', 'session:' .. sid)
end
for _, sid in ipairs(legacy_sessions) do
  if not seen[sid] then redis.call('DEL', 'session:' .. sid) end
end
redis.call('DEL', KEYS[1], KEYS[2])
return #sessions + #legacy_sessions`)

func (s *sessionStore) DeleteUser(ctx context.Context, userID uint64) error {
	return deleteUserSessionsScript.Run(ctx, s.client, []string{
		userSessionsKey(userID),
		legacyUserSessionsKey(userID),
	}).Err()
}
