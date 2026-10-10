package store

import (
	"context"
	"database/sql"
	"time"
)

type SettingsRepo struct{ db *sql.DB }

func NewSettingsRepo(db *sql.DB) *SettingsRepo { return &SettingsRepo{db: db} }

func (r *SettingsRepo) Set(ctx context.Context, key, value string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO server_settings(key, value, updated_at)
		VALUES(?, ?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().UnixMilli())
	return err
}

func (r *SettingsRepo) Get(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}
