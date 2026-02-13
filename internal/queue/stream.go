package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type AskJob struct {
	JobID      string    `json:"job_id"`
	ChatID     int64     `json:"chat_id"`
	ChatType   string    `json:"chat_type"`
	UserID     int64     `json:"user_id"`
	MessageID  int64     `json:"message_id"`
	Prompt     string    `json:"prompt"`
	PresetName string    `json:"preset_name"`
	EnqueuedAt time.Time `json:"enqueued_at"`
	Attempts   int       `json:"attempts"`
}

type StreamQueue struct {
	redis    *redis.Client
	stream   string
	group    string
	consumer string
	block    time.Duration
}

type Message struct {
	ID  string
	Job AskJob
}

func NewStreamQueue(rdb *redis.Client, stream, group, consumer string, block time.Duration) *StreamQueue {
	return &StreamQueue{
		redis:    rdb,
		stream:   stream,
		group:    group,
		consumer: consumer,
		block:    block,
	}
}

func (q *StreamQueue) EnsureGroup(ctx context.Context) error {
	if q == nil {
		return fmt.Errorf("queue is nil")
	}
	err := q.redis.XGroupCreateMkStream(ctx, q.stream, q.group, "$").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("create stream group: %w", err)
	}
	return nil
}

func (q *StreamQueue) Enqueue(ctx context.Context, job AskJob) (string, error) {
	if strings.TrimSpace(job.JobID) == "" {
		job.JobID = newJobID()
	}
	if job.EnqueuedAt.IsZero() {
		job.EnqueuedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return "", fmt.Errorf("marshal job: %w", err)
	}

	id, err := q.redis.XAdd(ctx, &redis.XAddArgs{
		Stream: q.stream,
		Values: map[string]any{"payload": payload},
	}).Result()
	if err != nil {
		return "", fmt.Errorf("enqueue: %w", err)
	}
	return id, nil
}

func (q *StreamQueue) Read(ctx context.Context, count int64) ([]Message, error) {
	res, err := q.redis.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    q.group,
		Consumer: q.consumer,
		Streams:  []string{q.stream, ">"},
		Count:    count,
		Block:    q.block,
		NoAck:    false,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("xreadgroup: %w", err)
	}

	out := make([]Message, 0)
	for _, s := range res {
		for _, m := range s.Messages {
			raw, ok := m.Values["payload"]
			if !ok {
				continue
			}

			var b []byte
			switch v := raw.(type) {
			case string:
				b = []byte(v)
			case []byte:
				b = v
			default:
				continue
			}

			var job AskJob
			if err := json.Unmarshal(b, &job); err != nil {
				continue
			}

			out = append(out, Message{ID: m.ID, Job: job})
		}
	}

	return out, nil
}

func (q *StreamQueue) Ack(ctx context.Context, messageID string) error {
	if err := q.redis.XAck(ctx, q.stream, q.group, messageID).Err(); err != nil {
		return fmt.Errorf("xack: %w", err)
	}
	if err := q.redis.XDel(ctx, q.stream, messageID).Err(); err != nil {
		return fmt.Errorf("xdel: %w", err)
	}
	return nil
}

func (q *StreamQueue) Consumer() string {
	return q.consumer
}

func newJobID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("job-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
