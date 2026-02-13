package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var incrWithTTLScript = redis.NewScript(`
local c = redis.call("INCR", KEYS[1])
if c == 1 then
  redis.call("EXPIRE", KEYS[1], ARGV[1])
end
return c
`)

type RateLimiter struct {
	redis *redis.Client
	limit int64
}

func NewRateLimiter(rdb *redis.Client, limit int64) *RateLimiter {
	return &RateLimiter{redis: rdb, limit: limit}
}

func (r *RateLimiter) Allow(ctx context.Context, chatID, userID int64, now time.Time) (allowed bool, used int64, resetAt time.Time, err error) {
	windowStart := now.UTC().Truncate(time.Hour)
	windowEnd := windowStart.Add(time.Hour)
	ttl := int64(windowEnd.Sub(now.UTC()).Seconds())
	if ttl < 1 {
		ttl = 1
	}

	key := fmt.Sprintf("hyprbot:ratelimit:%d:%d:%s", chatID, userID, windowStart.Format("2006010215"))
	res, err := incrWithTTLScript.Run(ctx, r.redis, []string{key}, ttl).Int64()
	if err != nil {
		return false, 0, time.Time{}, fmt.Errorf("rate limit script: %w", err)
	}
	return res <= r.limit, res, windowEnd, nil
}

type UpdateDeduplicator struct {
	redis *redis.Client
	ttl   time.Duration
}

func NewUpdateDeduplicator(rdb *redis.Client, ttl time.Duration) *UpdateDeduplicator {
	return &UpdateDeduplicator{redis: rdb, ttl: ttl}
}

func (d *UpdateDeduplicator) MarkFirst(ctx context.Context, updateID int64) (bool, error) {
	key := fmt.Sprintf("hyprbot:update:%d", updateID)
	ok, err := d.redis.SetNX(ctx, key, "1", d.ttl).Result()
	if err != nil {
		return false, fmt.Errorf("dedupe setnx: %w", err)
	}
	return ok, nil
}
