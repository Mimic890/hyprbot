#!/usr/bin/env bash
set -euo pipefail

OUT_FILE=".env"
FORCE=0

usage() {
  cat <<USAGE
Usage: $0 [--force] [--output <path>]

Creates a ready-to-run .env file with generated secrets.

Options:
  -f, --force          Overwrite existing file (creates timestamped backup)
  -o, --output <path>  Output path (default: .env)
  -h, --help           Show this help
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -f|--force)
      FORCE=1
      shift
      ;;
    -o|--output)
      if [[ $# -lt 2 ]]; then
        echo "error: --output requires a value" >&2
        exit 1
      fi
      OUT_FILE="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

rand_b64() {
  local bytes="$1"
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 "$bytes" | tr -d '\n'
  else
    head -c "$bytes" /dev/urandom | base64 | tr -d '\n'
  fi
}

rand_hex() {
  local bytes="$1"
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex "$bytes" | tr -d '\n'
  else
    od -An -tx1 -N"$bytes" /dev/urandom | tr -d ' \n'
  fi
}

rand_urlsafe() {
  local bytes="$1"
  local out
  if command -v openssl >/dev/null 2>&1; then
    out="$(openssl rand -base64 "$bytes" | tr '+/' '-_' | tr -d '=\n')"
  else
    out="$(head -c "$bytes" /dev/urandom | base64 | tr '+/' '-_' | tr -d '=\n')"
  fi
  printf '%s' "$out"
}

get_from_env_file() {
  local file="$1"
  local key="$2"
  if [[ ! -f "$file" ]]; then
    return 0
  fi
  grep -E "^${key}=" "$file" | head -n1 | cut -d= -f2- || true
}

if [[ -f "$OUT_FILE" && "$FORCE" -ne 1 ]]; then
  echo "error: $OUT_FILE already exists. Use --force to overwrite." >&2
  exit 1
fi

EXISTING_BOT_TOKEN=""
EXISTING_WEBHOOK_URL=""
EXISTING_BOT_ACCESS_MODE=""
EXISTING_ADMIN_USER_ID=""
if [[ -f "$OUT_FILE" ]]; then
  EXISTING_BOT_TOKEN="$(get_from_env_file "$OUT_FILE" "BOT_TOKEN")"
  EXISTING_WEBHOOK_URL="$(get_from_env_file "$OUT_FILE" "WEBHOOK_URL")"
  EXISTING_BOT_ACCESS_MODE="$(get_from_env_file "$OUT_FILE" "BOT_ACCESS_MODE")"
  EXISTING_ADMIN_USER_ID="$(get_from_env_file "$OUT_FILE" "ADMIN_USER_ID")"
  BACKUP_FILE="${OUT_FILE}.bak.$(date +%Y%m%d%H%M%S)"
  cp "$OUT_FILE" "$BACKUP_FILE"
  echo "backup created: $BACKUP_FILE"
fi

BOT_TOKEN="${EXISTING_BOT_TOKEN:-123456:replace_me}"
if [[ -z "$BOT_TOKEN" ]]; then
  BOT_TOKEN="123456:replace_me"
fi

WEBHOOK_URL="${EXISTING_WEBHOOK_URL:-https://example.com}"
if [[ -z "$WEBHOOK_URL" ]]; then
  WEBHOOK_URL="https://example.com"
fi

BOT_ACCESS_MODE="${EXISTING_BOT_ACCESS_MODE:-public}"
case "$BOT_ACCESS_MODE" in
  public|private) ;;
  *) BOT_ACCESS_MODE="private" ;;
esac

ADMIN_USER_ID="${EXISTING_ADMIN_USER_ID:-0}"
if ! [[ "$ADMIN_USER_ID" =~ ^[0-9]+$ ]]; then
  ADMIN_USER_ID="0"
fi

POSTGRES_DB="hyprbot"
POSTGRES_USER="postgres"
POSTGRES_PASSWORD="$(rand_urlsafe 32 | cut -c1-24)"
MASTER_KEY_B64="$(rand_b64 32)"
WEBHOOK_SECRET_PATH="$(rand_hex 32)"
WEBHOOK_SECRET_TOKEN="$(rand_urlsafe 48 | cut -c1-64)"

DB_DSN="postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable"

mkdir -p data/postgres data/redis data/bot

cat > "$OUT_FILE" <<ENV
BOT_TOKEN=$BOT_TOKEN
BOT_ACCESS_MODE=$BOT_ACCESS_MODE
ADMIN_USER_ID=$ADMIN_USER_ID

APP_MODE=ALL
DEV_POLLING=true

WEBHOOK_URL=$WEBHOOK_URL
WEBHOOK_SECRET_PATH=$WEBHOOK_SECRET_PATH
WEBHOOK_SECRET_TOKEN=$WEBHOOK_SECRET_TOKEN
WEBHOOK_LISTEN_ADDR=:8080

DB_DRIVER=postgres
POSTGRES_DB=$POSTGRES_DB
POSTGRES_USER=$POSTGRES_USER
POSTGRES_PASSWORD=$POSTGRES_PASSWORD
DB_DSN=$DB_DSN

REDIS_ADDR=redis:6379
REDIS_PASSWORD=
REDIS_DB=0

RATE_LIMIT_PER_HOUR=30

MASTER_KEY_B64=$MASTER_KEY_B64
# rotation alternative:
# MASTER_KEY_CURRENT_ID=k2026_01
# MASTER_KEY_k2026_01_B64=...
# MASTER_KEY_k2025_12_B64=...

LOG_LEVEL=info
ENV

echo "generated: $OUT_FILE"
if [[ "$BOT_TOKEN" == "123456:replace_me" ]]; then
  echo "next step: set BOT_TOKEN in $OUT_FILE"
fi
if [[ "$BOT_ACCESS_MODE" == "private" && "$ADMIN_USER_ID" == "0" ]]; then
  echo "next step: set ADMIN_USER_ID in $OUT_FILE"
fi
echo "data dirs ready: data/postgres data/redis data/bot"
echo "done"
