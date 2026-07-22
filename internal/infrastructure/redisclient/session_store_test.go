package redisclient

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestSessionRotationRenewsUserSessionIndex(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewSessionStore(client)
	ctx := context.Background()
	const (
		sessionID = "session-1"
		oldHash   = "old-hash"
		newHash   = "new-hash"
		userID    = uint64(7)
	)
	ttl := time.Hour
	if err := store.Create(ctx, sessionID, userID, oldHash, ttl); err != nil {
		t.Fatal(err)
	}
	server.FastForward(30 * time.Minute)
	rotated, err := store.Rotate(ctx, sessionID, oldHash, newHash, ttl)
	if err != nil || !rotated {
		t.Fatalf("Rotate rotated=%v err=%v", rotated, err)
	}
	remaining, err := client.TTL(ctx, userSessionsKey(userID)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if remaining > ttl || remaining < ttl-2*time.Second {
		t.Fatalf("user session index TTL=%s want within two seconds of %s", remaining, ttl)
	}
	if err = store.DeleteUser(ctx, userID); err != nil {
		t.Fatal(err)
	}
	exists, err := store.Exists(ctx, sessionID)
	if err != nil || exists {
		t.Fatalf("session remains after DeleteUser: exists=%v err=%v", exists, err)
	}
}

func TestSessionIndexPrunesExpiredEntriesAndEnforcesLimit(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewSessionStore(client)
	ctx := context.Background()
	const userID = uint64(11)
	clock := time.Unix(1_700_000_000, 0)
	server.SetTime(clock)

	if err := store.Create(ctx, "long", userID, "hash", 2*time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(ctx, "short", userID, "hash", time.Hour); err != nil {
		t.Fatal(err)
	}
	server.FastForward(90 * time.Minute)
	server.SetTime(clock.Add(90 * time.Minute))
	if err := store.Create(ctx, "replacement", userID, "hash", 2*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ZScore(ctx, userSessionsKey(userID), "short").Result(); !errors.Is(err, redis.Nil) {
		t.Fatalf("expired session remains in index: %v", err)
	}
	if count, err := client.ZCard(ctx, userSessionsKey(userID)).Result(); err != nil || count != 2 {
		t.Fatalf("pruned index count=%d err=%v", count, err)
	}

	for i := range maxActiveSessionsPerUser + 5 {
		sid := fmt.Sprintf("bounded-%02d", i)
		if err := store.Create(ctx, sid, userID, "hash", 2*time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if count, err := client.ZCard(ctx, userSessionsKey(userID)).Result(); err != nil || count != maxActiveSessionsPerUser {
		t.Fatalf("bounded index count=%d err=%v", count, err)
	}
	activeHashes := 0
	for i := range maxActiveSessionsPerUser + 5 {
		sid := fmt.Sprintf("bounded-%02d", i)
		exists, err := store.Exists(ctx, sid)
		if err != nil {
			t.Fatal(err)
		}
		if exists {
			activeHashes++
		}
	}
	if activeHashes > maxActiveSessionsPerUser {
		t.Fatalf("active session hashes=%d want <=%d", activeHashes, maxActiveSessionsPerUser)
	}
}

func TestDeleteUserAtomicallyCleansCurrentAndLegacyIndexes(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewSessionStore(client)
	ctx := context.Background()
	const userID = uint64(19)
	if err := store.Create(ctx, "current", userID, "hash", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, key("legacy"), "user_id", userID, "refresh_hash", "hash").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(ctx, legacyUserSessionsKey(userID), "legacy").Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteUser(ctx, userID); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"current", "legacy"} {
		exists, err := store.Exists(ctx, sid)
		if err != nil || exists {
			t.Fatalf("session %q remains: exists=%v err=%v", sid, exists, err)
		}
	}
	if count, err := client.Exists(ctx, userSessionsKey(userID), legacyUserSessionsKey(userID)).Result(); err != nil || count != 0 {
		t.Fatalf("session indexes remain: count=%d err=%v", count, err)
	}
}
