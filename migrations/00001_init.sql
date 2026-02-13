-- +goose Up
CREATE TABLE IF NOT EXISTS chats (
    id BIGINT PRIMARY KEY,
    type TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    default_preset_name TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS chat_admin_cache (
    chat_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    is_admin BOOLEAN NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (chat_id, user_id)
);

CREATE TABLE IF NOT EXISTS provider_instances (
    id BIGSERIAL PRIMARY KEY,
    chat_id BIGINT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    base_url TEXT NOT NULL,
    enc_api_key JSONB,
    enc_headers_json JSONB,
    config_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(chat_id, name)
);

CREATE TABLE IF NOT EXISTS presets (
    chat_id BIGINT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    provider_instance_id BIGINT NOT NULL REFERENCES provider_instances(id) ON DELETE CASCADE,
    model TEXT NOT NULL,
    system_prompt TEXT NOT NULL DEFAULT '',
    params_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (chat_id, name)
);

CREATE TABLE IF NOT EXISTS audit_log (
    id BIGSERIAL PRIMARY KEY,
    chat_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    action TEXT NOT NULL,
    meta_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_provider_instances_chat_id ON provider_instances(chat_id);
CREATE INDEX IF NOT EXISTS idx_presets_chat_id ON presets(chat_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_chat_id_created_at ON audit_log(chat_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS presets;
DROP TABLE IF EXISTS provider_instances;
DROP TABLE IF EXISTS chat_admin_cache;
DROP TABLE IF EXISTS chats;
