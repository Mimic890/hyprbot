package queue

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRateLimiterAllow(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	rl := NewRateLimiter(rdb, 2)
	now := time.Date(2026, 2, 13, 10, 0, 0, 0, time.UTC)

	allowed, used, _, err := rl.Allow(context.Background(), 1, 10, now)
	if err != nil {
		t.Fatalf("allow#1: %v", err)
	}
	if !allowed || used != 1 {
		t.Fatalf("expected first call allowed with used=1, got allowed=%v used=%d", allowed, used)
	}

	allowed, used, _, err = rl.Allow(context.Background(), 1, 10, now)
	if err != nil {
		t.Fatalf("allow#2: %v", err)
	}
	if !allowed || used != 2 {
		t.Fatalf("expected second call allowed with used=2, got allowed=%v used=%d", allowed, used)
	}

	allowed, used, _, err = rl.Allow(context.Background(), 1, 10, now)
	if err != nil {
		t.Fatalf("allow#3: %v", err)
	}
	if allowed || used != 3 {
		t.Fatalf("expected third call denied with used=3, got allowed=%v used=%d", allowed, used)
	}
}
