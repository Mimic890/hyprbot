package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sq "github.com/Masterminds/squirrel"
)

var ErrNotFound = errors.New("not found")

func (s *Store) EnsureChat(ctx context.Context, chatID int64, chatType, title string) error {
	if chatType == "" {
		chatType = "unknown"
	}
	q := s.sql.Insert("chats").
		Columns("id", "type", "title").
		Values(chatID, chatType, title).
		Suffix("ON CONFLICT(id) DO UPDATE SET type=excluded.type, title=excluded.title")

	sqlStr, args, err := q.ToSql()
	if err != nil {
		return fmt.Errorf("build ensure chat query: %w", err)
	}
	_, err = s.db.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return fmt.Errorf("ensure chat: %w", err)
	}
	return nil
}

func (s *Store) SetAdminCache(ctx context.Context, chatID, userID int64, isAdmin bool) error {
	q := s.sql.Insert("chat_admin_cache").
		Columns("chat_id", "user_id", "is_admin", "updated_at").
		Values(chatID, userID, isAdmin, nowExpr(s.driver)).
		Suffix("ON CONFLICT(chat_id, user_id) DO UPDATE SET is_admin=excluded.is_admin, updated_at=excluded.updated_at")

	sqlStr, args, err := q.ToSql()
	if err != nil {
		return fmt.Errorf("build set admin cache query: %w", err)
	}
	_, err = s.db.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return fmt.Errorf("set admin cache: %w", err)
	}
	return nil
}

func (s *Store) GetAdminCache(ctx context.Context, chatID, userID int64) (isAdmin bool, found bool, err error) {
	q := s.sql.Select("is_admin").
		From("chat_admin_cache").
		Where(sq.Eq{"chat_id": chatID, "user_id": userID})
	query, args, err := q.ToSql()
	if err != nil {
		return false, false, fmt.Errorf("build get admin cache query: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&isAdmin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("get admin cache: %w", err)
	}
	return isAdmin, true, nil
}

func (s *Store) UpsertProviderInstance(ctx context.Context, p ProviderInstance) (int64, error) {
	if p.ConfigJSON == "" {
		p.ConfigJSON = "{}"
	}
	q := s.sql.Insert("provider_instances").
		Columns("chat_id", "name", "kind", "base_url", "enc_api_key", "enc_headers_json", "config_json").
		Values(p.ChatID, p.Name, p.Kind, p.BaseURL, p.EncAPIKey, p.EncHeadersJSON, p.ConfigJSON).
		Suffix("ON CONFLICT(chat_id, name) DO UPDATE SET kind=excluded.kind, base_url=excluded.base_url, enc_api_key=excluded.enc_api_key, enc_headers_json=excluded.enc_headers_json, config_json=excluded.config_json")

	sqlStr, args, err := q.ToSql()
	if err != nil {
		return 0, fmt.Errorf("build provider upsert query: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, sqlStr, args...); err != nil {
		return 0, fmt.Errorf("upsert provider: %w", err)
	}

	return s.GetProviderInstanceID(ctx, p.ChatID, p.Name)
}

func (s *Store) GetProviderInstanceID(ctx context.Context, chatID int64, name string) (int64, error) {
	q := s.sql.Select("id").From("provider_instances").Where(sq.Eq{"chat_id": chatID, "name": name})
	sqlStr, args, err := q.ToSql()
	if err != nil {
		return 0, fmt.Errorf("build provider id query: %w", err)
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, sqlStr, args...).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("get provider id: %w", err)
	}
	return id, nil
}

func (s *Store) GetProviderByName(ctx context.Context, chatID int64, name string) (ProviderInstance, error) {
	q := s.sql.Select("id", "chat_id", "name", "kind", "base_url", "enc_api_key", "enc_headers_json", "config_json", "created_at").
		From("provider_instances").
		Where(sq.Eq{"chat_id": chatID, "name": name})
	sqlStr, args, err := q.ToSql()
	if err != nil {
		return ProviderInstance{}, fmt.Errorf("build provider by name query: %w", err)
	}

	var p ProviderInstance
	var encAPIKey, encHeaders sql.NullString
	if err := s.db.QueryRowContext(ctx, sqlStr, args...).Scan(
		&p.ID,
		&p.ChatID,
		&p.Name,
		&p.Kind,
		&p.BaseURL,
		&encAPIKey,
		&encHeaders,
		&p.ConfigJSON,
		&p.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProviderInstance{}, ErrNotFound
		}
		return ProviderInstance{}, fmt.Errorf("get provider by name: %w", err)
	}
	if encAPIKey.Valid {
		p.EncAPIKey = &encAPIKey.String
	}
	if encHeaders.Valid {
		p.EncHeadersJSON = &encHeaders.String
	}
	return p, nil
}

func (s *Store) GetProviderByID(ctx context.Context, chatID int64, providerID int64) (ProviderInstance, error) {
	q := s.sql.Select("id", "chat_id", "name", "kind", "base_url", "enc_api_key", "enc_headers_json", "config_json", "created_at").
		From("provider_instances").
		Where(sq.Eq{"chat_id": chatID, "id": providerID})
	sqlStr, args, err := q.ToSql()
	if err != nil {
		return ProviderInstance{}, fmt.Errorf("build provider by id query: %w", err)
	}

	var p ProviderInstance
	var encAPIKey, encHeaders sql.NullString
	if err := s.db.QueryRowContext(ctx, sqlStr, args...).Scan(
		&p.ID,
		&p.ChatID,
		&p.Name,
		&p.Kind,
		&p.BaseURL,
		&encAPIKey,
		&encHeaders,
		&p.ConfigJSON,
		&p.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProviderInstance{}, ErrNotFound
		}
		return ProviderInstance{}, fmt.Errorf("get provider by id: %w", err)
	}
	if encAPIKey.Valid {
		p.EncAPIKey = &encAPIKey.String
	}
	if encHeaders.Valid {
		p.EncHeadersJSON = &encHeaders.String
	}
	return p, nil
}

func (s *Store) ListProviders(ctx context.Context, chatID int64) ([]ProviderInstance, error) {
	q := s.sql.Select("id", "chat_id", "name", "kind", "base_url", "enc_api_key", "enc_headers_json", "config_json", "created_at").
		From("provider_instances").
		Where(sq.Eq{"chat_id": chatID}).
		OrderBy("created_at ASC")
	sqlStr, args, err := q.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build list providers query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()

	out := make([]ProviderInstance, 0)
	for rows.Next() {
		var p ProviderInstance
		var encAPIKey, encHeaders sql.NullString
		if err := rows.Scan(
			&p.ID,
			&p.ChatID,
			&p.Name,
			&p.Kind,
			&p.BaseURL,
			&encAPIKey,
			&encHeaders,
			&p.ConfigJSON,
			&p.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan provider row: %w", err)
		}
		if encAPIKey.Valid {
			p.EncAPIKey = &encAPIKey.String
		}
		if encHeaders.Valid {
			p.EncHeadersJSON = &encHeaders.String
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provider rows: %w", err)
	}
	return out, nil
}

func (s *Store) DeleteProviderByName(ctx context.Context, chatID int64, name string) error {
	q := s.sql.Delete("provider_instances").Where(sq.Eq{"chat_id": chatID, "name": name})
	sqlStr, args, err := q.ToSql()
	if err != nil {
		return fmt.Errorf("build delete provider query: %w", err)
	}
	res, err := s.db.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return fmt.Errorf("delete provider: %w", err)
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpsertPreset(ctx context.Context, p Preset) error {
	if p.ParamsJSON == "" {
		p.ParamsJSON = "{}"
	}
	q := s.sql.Insert("presets").
		Columns("chat_id", "name", "provider_instance_id", "model", "system_prompt", "params_json").
		Values(p.ChatID, p.Name, p.ProviderInstanceID, p.Model, p.SystemPrompt, p.ParamsJSON).
		Suffix("ON CONFLICT(chat_id, name) DO UPDATE SET provider_instance_id=excluded.provider_instance_id, model=excluded.model, system_prompt=excluded.system_prompt, params_json=excluded.params_json")

	sqlStr, args, err := q.ToSql()
	if err != nil {
		return fmt.Errorf("build preset upsert query: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, sqlStr, args...); err != nil {
		return fmt.Errorf("upsert preset: %w", err)
	}
	return nil
}

func (s *Store) DeletePreset(ctx context.Context, chatID int64, name string) error {
	q := s.sql.Delete("presets").Where(sq.Eq{"chat_id": chatID, "name": name})
	sqlStr, args, err := q.ToSql()
	if err != nil {
		return fmt.Errorf("build delete preset query: %w", err)
	}
	res, err := s.db.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return fmt.Errorf("delete preset: %w", err)
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetDefaultPreset(ctx context.Context, chatID int64, name string) error {
	q := s.sql.Update("chats").
		Set("default_preset_name", name).
		Where(sq.Eq{"id": chatID})
	sqlStr, args, err := q.ToSql()
	if err != nil {
		return fmt.Errorf("build set default preset query: %w", err)
	}
	res, err := s.db.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return fmt.Errorf("set default preset: %w", err)
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ClearDefaultPreset(ctx context.Context, chatID int64) error {
	q := s.sql.Update("chats").
		Set("default_preset_name", nil).
		Where(sq.Eq{"id": chatID})
	sqlStr, args, err := q.ToSql()
	if err != nil {
		return fmt.Errorf("build clear default preset query: %w", err)
	}
	_, err = s.db.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return fmt.Errorf("clear default preset: %w", err)
	}
	return nil
}

func (s *Store) GetDefaultPresetName(ctx context.Context, chatID int64) (string, error) {
	q := s.sql.Select("default_preset_name").From("chats").Where(sq.Eq{"id": chatID})
	sqlStr, args, err := q.ToSql()
	if err != nil {
		return "", fmt.Errorf("build default preset name query: %w", err)
	}
	var name sql.NullString
	if err := s.db.QueryRowContext(ctx, sqlStr, args...).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get default preset name: %w", err)
	}
	if !name.Valid || strings.TrimSpace(name.String) == "" {
		return "", ErrNotFound
	}
	return name.String, nil
}

func (s *Store) ListPresets(ctx context.Context, chatID int64) ([]Preset, error) {
	q := s.sql.Select("chat_id", "name", "provider_instance_id", "model", "system_prompt", "params_json", "created_at").
		From("presets").
		Where(sq.Eq{"chat_id": chatID}).
		OrderBy("created_at ASC")
	sqlStr, args, err := q.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build list presets query: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("list presets: %w", err)
	}
	defer rows.Close()

	out := make([]Preset, 0)
	for rows.Next() {
		var p Preset
		if err := rows.Scan(&p.ChatID, &p.Name, &p.ProviderInstanceID, &p.Model, &p.SystemPrompt, &p.ParamsJSON, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan preset row: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate preset rows: %w", err)
	}
	return out, nil
}

func (s *Store) GetPresetWithProviderByName(ctx context.Context, chatID int64, name string) (PresetWithProvider, error) {
	return s.getPresetWithProvider(ctx, sq.Eq{"p.chat_id": chatID, "p.name": name})
}

func (s *Store) GetDefaultPresetWithProvider(ctx context.Context, chatID int64) (PresetWithProvider, error) {
	q := s.sql.Select("default_preset_name").From("chats").Where(sq.Eq{"id": chatID})
	sqlStr, args, err := q.ToSql()
	if err != nil {
		return PresetWithProvider{}, fmt.Errorf("build default preset query: %w", err)
	}
	var name sql.NullString
	if err := s.db.QueryRowContext(ctx, sqlStr, args...).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PresetWithProvider{}, ErrNotFound
		}
		return PresetWithProvider{}, fmt.Errorf("get default preset: %w", err)
	}
	if !name.Valid || strings.TrimSpace(name.String) == "" {
		return PresetWithProvider{}, ErrNotFound
	}
	return s.GetPresetWithProviderByName(ctx, chatID, name.String)
}

func (s *Store) getPresetWithProvider(ctx context.Context, where sq.Sqlizer) (PresetWithProvider, error) {
	q := s.sql.Select(
		"p.chat_id", "p.name", "p.provider_instance_id", "p.model", "p.system_prompt", "p.params_json", "p.created_at",
		"pr.id", "pr.chat_id", "pr.name", "pr.kind", "pr.base_url", "pr.enc_api_key", "pr.enc_headers_json", "pr.config_json", "pr.created_at",
	).From("presets p").
		Join("provider_instances pr ON p.provider_instance_id = pr.id").
		Where(where)

	sqlStr, args, err := q.ToSql()
	if err != nil {
		return PresetWithProvider{}, fmt.Errorf("build preset with provider query: %w", err)
	}

	var out PresetWithProvider
	var encAPIKey, encHeaders sql.NullString
	if err := s.db.QueryRowContext(ctx, sqlStr, args...).Scan(
		&out.Preset.ChatID,
		&out.Preset.Name,
		&out.Preset.ProviderInstanceID,
		&out.Preset.Model,
		&out.Preset.SystemPrompt,
		&out.Preset.ParamsJSON,
		&out.Preset.CreatedAt,
		&out.Provider.ID,
		&out.Provider.ChatID,
		&out.Provider.Name,
		&out.Provider.Kind,
		&out.Provider.BaseURL,
		&encAPIKey,
		&encHeaders,
		&out.Provider.ConfigJSON,
		&out.Provider.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PresetWithProvider{}, ErrNotFound
		}
		return PresetWithProvider{}, fmt.Errorf("get preset with provider: %w", err)
	}
	if encAPIKey.Valid {
		out.Provider.EncAPIKey = &encAPIKey.String
	}
	if encHeaders.Valid {
		out.Provider.EncHeadersJSON = &encHeaders.String
	}
	return out, nil
}

func (s *Store) LogAction(ctx context.Context, e AuditEntry) error {
	if strings.TrimSpace(e.MetaJSON) == "" {
		e.MetaJSON = "{}"
	}
	if !json.Valid([]byte(e.MetaJSON)) {
		e.MetaJSON = "{}"
	}

	q := s.sql.Insert("audit_log").
		Columns("chat_id", "user_id", "action", "meta_json").
		Values(e.ChatID, e.UserID, e.Action, e.MetaJSON)
	sqlStr, args, err := q.ToSql()
	if err != nil {
		return fmt.Errorf("build audit insert query: %w", err)
	}
	_, err = s.db.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		return fmt.Errorf("insert audit entry: %w", err)
	}
	return nil
}

func nowExpr(driver string) any {
	if driver == "postgres" {
		return sq.Expr("NOW()")
	}
	return sq.Expr("CURRENT_TIMESTAMP")
}
