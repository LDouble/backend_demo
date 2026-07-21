package redisclient

import (
	"context"
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
	if remaining != ttl {
		t.Fatalf("user session index TTL=%s want=%s", remaining, ttl)
	}
	if err = store.DeleteUser(ctx, userID); err != nil {
		t.Fatal(err)
	}
	exists, err := store.Exists(ctx, sessionID)
	if err != nil || exists {
		t.Fatalf("session remains after DeleteUser: exists=%v err=%v", exists, err)
	}
}
