# hyprbot

Production-ready Telegram bot in Go (1.22+) with horizontally scalable architecture:
- ingress mode (`webhook`) accepts Telegram updates and **only enqueues jobs**
- worker mode (`worker`) consumes Redis queue, calls LLM providers, replies to Telegram
- combined mode (`all`) runs both in one process/container

Built on `gotgbot` updater/dispatcher, Redis, Postgres, AES-256-GCM envelope encryption, and per-chat multi-tenant config.

## Features

- Horizontal scale: multiple webhook replicas + multiple worker replicas
- Idempotency: dedupe by `update_id` in Redis (`SETNX + TTL`)
- Multi-tenant: providers/presets scoped per chat
- RBAC: only chat admins can mutate providers/presets (`getChatMember`)
- Secure provider key onboarding: `/llm_add` in group redirects admin to DM wizard via deep-link
- Secrets encryption in DB only: envelope JSON `{key_id, nonce, ciphertext}`
- Key rotation support:
  - `MASTER_KEY_CURRENT_ID` + `MASTER_KEYS_JSON`
  - or `MASTER_KEY_<ID>_B64` vars
  - or fallback `MASTER_KEY_B64`
- Rate limit per user per chat in Redis (N/hour)
- Structured logs (zerolog), `/healthz`, `/metrics`
- No paywall/subscription logic; pure OSS behavior

## Repository Layout

- `cmd/bot/main.go`
- `internal/config`
- `internal/telegram`
- `internal/storage`
- `internal/crypto`
- `internal/queue`
- `internal/providers/openai_compat`
- `internal/providers/custom_http`
- `internal/providers/openai_responses` (stub)
- `internal/providers/anthropic_messages` (stub)
- `internal/worker`
- `migrations`

## Commands

User:
- `/help`
- `/ask <text>`
- `/ai <preset> <text>`
- `/ai_list`

Admin (group/supergroup only):
- `/ai_preset_add <name> <provider> <model> <system_prompt...>`
- `/ai_preset_del <name>`
- `/ai_default <name>`
- `/llm_add`
- `/llm_list`
- `/llm_del <name>`

## Local Run (fish)

### 1) Dependencies

```fish
# Postgres + Redis locally (example via docker)
docker run --name hyprbot-postgres -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=hyprbot -p 5432:5432 -d postgres:16

docker run --name hyprbot-redis -p 6379:6379 -d redis:7
```

### 2) Environment

```fish
set -x BOT_TOKEN "<telegram_bot_token>"
set -x APP_MODE ALL
set -x DEV_POLLING true

set -x DB_DRIVER postgres
set -x DB_DSN "postgres://postgres:postgres@127.0.0.1:5432/hyprbot?sslmode=disable"

set -x REDIS_ADDR "127.0.0.1:6379"
set -x RATE_LIMIT_PER_HOUR 30

# one-key mode
set -x MASTER_KEY_B64 (openssl rand -base64 32 | tr -d '\n')

# optional rotation mode instead:
# set -x MASTER_KEY_CURRENT_ID "k2026_01"
# set -x MASTER_KEY_k2026_01_B64 (openssl rand -base64 32 | tr -d '\n')
# set -x MASTER_KEY_k2025_12_B64 "<old-key-b64>"
```

### 3) Start bot

```fish
go run ./cmd/bot
```

With `DEV_POLLING=true`, bot uses long-polling (good for local dev). Jobs are still queued in Redis and handled by worker path.

## Webhook Setup

Set these vars (fish):

```fish
set -x APP_MODE ALL
set -x DEV_POLLING false
set -x WEBHOOK_URL "https://your-domain.example"
set -x WEBHOOK_SECRET_PATH "telegram-super-secret-path"
set -x WEBHOOK_SECRET_TOKEN "telegram_header_secret_token"
set -x WEBHOOK_LISTEN_ADDR ":8080"
```

Then run:

```fish
go run ./cmd/bot
```

Bot calls Telegram `setWebhook` automatically to:
- `WEBHOOK_URL/WEBHOOK_SECRET_PATH`

Health and metrics:
- `GET /healthz`
- `GET /metrics`

## Docker

### docker-compose (dev)

```fish
cp .env.example .env
# edit BOT_TOKEN and MASTER_KEY_B64 in .env

docker compose up --build
```

Default compose runs `APP_MODE=ALL` with Postgres + Redis.
For quick local testing compose sets `DEV_POLLING=true` by default.
Set `DEV_POLLING=false` + `WEBHOOK_URL` for real webhook flow.

## Example: Add Grok (xAI) via OpenAI-compatible

1. In target group (admin only):

```text
/llm_add
```

2. Bot replies with DM deep-link (`/start llmadd_<chat_id>`). Open it.

3. In DM wizard, send sequentially:

```text
openai-compat
grok
https://api.x.ai/v1
chat_completions
<xai_api_key>
```

4. Back in group, create preset and make it default:

```text
/ai_preset_add grok_default grok grok-2-latest You are a concise assistant.
/ai_default grok_default
```

5. Ask:

```text
/ask Summarize this thread in 5 bullets.
```

## Security Notes

- Bot does **not** store user message history in DB by default.
- Provider secrets are stored encrypted only.
- Secret fields are never printed to logs by design.
- Webhook ingress does not block on heavy LLM calls.

## Testing

```fish
go test ./...
```

Included tests:
- `internal/crypto`: encrypt/decrypt/rotation
- `internal/providers/openai_compat`: request/payload build
- `internal/queue`: rate-limit logic
