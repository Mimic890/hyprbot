package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

type Store struct {
	db     *sql.DB
	driver string
	sql    sq.StatementBuilderType
}

func Open(ctx context.Context, driver, dsn string, autoMigrate bool, migrationsDir string) (*Store, error) {
	driver = normalizeDriver(driver)
	if dsn == "" {
		return nil, fmt.Errorf("dsn is empty")
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	if autoMigrate {
		switch driver {
		case "postgres":
			if migrationsDir == "" {
				migrationsDir = "migrations"
			}
			if err := goose.SetDialect("postgres"); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("set goose dialect: %w", err)
			}
			if err := goose.Up(db, migrationsDir); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("run migrations: %w", err)
			}
		case "sqlite":
			if err := initSQLiteSchema(ctx, db); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("init sqlite schema: %w", err)
			}
		default:
			_ = db.Close()
			return nil, fmt.Errorf("unsupported driver %q", driver)
		}
	}

	var placeholder sq.PlaceholderFormat = sq.Question
	if driver == "postgres" {
		placeholder = sq.Dollar
	}

	return &Store{
		db:     db,
		driver: driver,
		sql:    sq.StatementBuilder.PlaceholderFormat(placeholder),
	}, nil
}

func normalizeDriver(driver string) string {
	d := strings.ToLower(strings.TrimSpace(driver))
	switch d {
	case "postgres", "pgx":
		return "postgres"
	case "sqlite", "sqlite3":
		return "sqlite"
	default:
		return d
	}
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func initSQLiteSchema(ctx context.Context, db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS chats (
    id INTEGER PRIMARY KEY,
    type TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    default_preset_name TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS chat_admin_cache (
    chat_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    is_admin INTEGER NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (chat_id, user_id)
);
CREATE TABLE IF NOT EXISTS provider_instances (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    base_url TEXT NOT NULL,
    enc_api_key TEXT,
    enc_headers_json TEXT,
    config_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(chat_id, name)
);
CREATE TABLE IF NOT EXISTS presets (
    chat_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    provider_instance_id INTEGER NOT NULL,
    model TEXT NOT NULL,
    system_prompt TEXT NOT NULL DEFAULT '',
    params_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (chat_id, name)
);
CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    action TEXT NOT NULL,
    meta_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_provider_instances_chat_id ON provider_instances(chat_id);
CREATE INDEX IF NOT EXISTS idx_presets_chat_id ON presets(chat_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_chat_id_created_at ON audit_log(chat_id, created_at DESC);
`
	_, err := db.ExecContext(ctx, schema)
	return err
}
