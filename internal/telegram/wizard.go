package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type llmWizardState struct {
	TargetChatID int64  `json:"target_chat_id"`
	Step         string `json:"step"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	BaseURL      string `json:"base_url"`
	Endpoint     string `json:"endpoint"`
	HeadersJSON  string `json:"headers_json"`
}

type wizardStore struct {
	redis *redis.Client
	ttl   time.Duration
}

func newWizardStore(rdb *redis.Client, ttl time.Duration) *wizardStore {
	return &wizardStore{redis: rdb, ttl: ttl}
}

func (w *wizardStore) key(userID int64) string {
	return fmt.Sprintf("hyprbot:wizard:%d", userID)
}

func (w *wizardStore) Set(ctx context.Context, userID int64, state llmWizardState) error {
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return w.redis.Set(ctx, w.key(userID), string(b), w.ttl).Err()
}

func (w *wizardStore) Get(ctx context.Context, userID int64) (*llmWizardState, error) {
	raw, err := w.redis.Get(ctx, w.key(userID)).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state llmWizardState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (w *wizardStore) Clear(ctx context.Context, userID int64) error {
	return w.redis.Del(ctx, w.key(userID)).Err()
}
