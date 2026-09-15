package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (s *Store) FirstUser(ctx context.Context) (User, error) {
	var user User
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,username,password_hash,created_at,updated_at FROM users ORDER BY created_at LIMIT 1`).Scan(&user.ID, &user.Username, &user.PasswordHash, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	user.CreatedAt, _ = parseTime(created)
	user.UpdatedAt, _ = parseTime(updated)
	return user, nil
}

func (s *Store) GitHubOAuthSettings(ctx context.Context) (GitHubOAuthSettings, error) {
	var item GitHubOAuthSettings
	var enabled int
	var allowlist, updated string
	var verified sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT enabled,client_id,client_secret_encrypted,callback_url,username_allowlist_json,verified_at,updated_at FROM github_oauth_settings WHERE id=1`).Scan(&enabled, &item.ClientID, &item.ClientSecretEncrypted, &item.CallbackURL, &allowlist, &verified, &updated)
	if err != nil {
		return item, err
	}
	item.Enabled = enabled != 0
	item.ClientSecretSet = item.ClientSecretEncrypted != ""
	_ = json.Unmarshal([]byte(allowlist), &item.UsernameAllowlist)
	item.UpdatedAt, _ = parseTime(updated)
	if verified.Valid {
		value, _ := parseTime(verified.String)
		item.VerifiedAt = &value
	}
	return item, nil
}

func (s *Store) UpdateGitHubOAuthSettings(ctx context.Context, enabled bool, clientID string, secret *string, callbackURL string, allowlist []string) (GitHubOAuthSettings, error) {
	raw, _ := json.Marshal(allowlist)
	var result sql.Result
	var err error
	if secret == nil {
		result, err = s.db.ExecContext(ctx, `UPDATE github_oauth_settings SET enabled=?,client_id=?,callback_url=?,username_allowlist_json=?,updated_at=? WHERE id=1`, enabled, clientID, callbackURL, string(raw), nowText())
	} else {
		result, err = s.db.ExecContext(ctx, `UPDATE github_oauth_settings SET enabled=?,client_id=?,client_secret_encrypted=?,callback_url=?,username_allowlist_json=?,verified_at=NULL,updated_at=? WHERE id=1`, enabled, clientID, *secret, callbackURL, string(raw), nowText())
	}
	if err != nil {
		return GitHubOAuthSettings{}, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return GitHubOAuthSettings{}, ErrNotFound
	}
	return s.GitHubOAuthSettings(ctx)
}

func (s *Store) MarkGitHubOAuthVerified(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE github_oauth_settings SET verified_at=?,updated_at=? WHERE id=1`, formatTime(now), formatTime(now))
	return err
}

func (s *Store) CreateOAuthState(ctx context.Context, hash, provider string, expires, now time.Time) error {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM oauth_states WHERE expires_at<=?`, formatTime(now))
	_, err := s.db.ExecContext(ctx, `INSERT INTO oauth_states(state_hash,provider,expires_at,created_at) VALUES(?,?,?,?)`, hash, provider, formatTime(expires), formatTime(now))
	return err
}

func (s *Store) ConsumeOAuthState(ctx context.Context, hash, provider string, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var stored string
	err = tx.QueryRowContext(ctx, `SELECT state_hash FROM oauth_states WHERE state_hash=? AND provider=? AND expires_at>?`, hash, strings.ToLower(provider), formatTime(now)).Scan(&stored)
	_, _ = tx.ExecContext(ctx, `DELETE FROM oauth_states WHERE state_hash=?`, hash)
	if commitErr := tx.Commit(); commitErr != nil {
		return false, commitErr
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
