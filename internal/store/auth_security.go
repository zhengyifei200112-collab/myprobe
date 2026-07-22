package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	var user User
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,username,password_hash,created_at,updated_at FROM users WHERE id=?`, id).Scan(&user.ID, &user.Username, &user.PasswordHash, &created, &updated)
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

func (s *Store) UpdateUserPassword(ctx context.Context, userID, passwordHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE users SET password_hash=?,updated_at=? WHERE id=?`, passwordHash, nowText(), userID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) LoginGuard(ctx context.Context, username, remoteIP string, now time.Time) (LoginGuard, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	windowStart := formatTime(now.Add(-15 * time.Minute))
	var pairCount, ipCount int
	var pairLatest, ipLatest sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),MAX(created_at) FROM login_failures WHERE username=? AND remote_ip=? AND julianday(created_at)>=julianday(?)`, username, remoteIP, windowStart).Scan(&pairCount, &pairLatest); err != nil {
		return LoginGuard{}, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),MAX(created_at) FROM login_failures WHERE remote_ip=? AND julianday(created_at)>=julianday(?)`, remoteIP, windowStart).Scan(&ipCount, &ipLatest); err != nil {
		return LoginGuard{}, err
	}
	guard := LoginGuard{CaptchaRequired: pairCount >= 3 || ipCount >= 5}
	if pairCount >= 5 || ipCount >= 10 {
		latest := time.Time{}
		for _, raw := range []sql.NullString{pairLatest, ipLatest} {
			if raw.Valid {
				if value, err := parseTime(raw.String); err == nil && value.After(latest) {
					latest = value
				}
			}
		}
		if !latest.IsZero() {
			blocked := latest.Add(15 * time.Minute)
			if blocked.After(now) {
				guard.BlockedUntil = &blocked
			}
		}
	}
	return guard, nil
}

func (s *Store) RecordLoginFailure(ctx context.Context, username, remoteIP string, now time.Time) error {
	username = strings.ToLower(strings.TrimSpace(username))
	_, err := s.db.ExecContext(ctx, `INSERT INTO login_failures(username,remote_ip,created_at) VALUES(?,?,?)`, username, remoteIP, formatTime(now))
	if err == nil {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM login_failures WHERE julianday(created_at)<julianday(?)`, formatTime(now.Add(-24*time.Hour)))
	}
	return err
}

func (s *Store) ClearLoginFailures(ctx context.Context, username, remoteIP string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM login_failures WHERE username=? AND remote_ip=?`, strings.ToLower(strings.TrimSpace(username)), remoteIP)
	return err
}

func (s *Store) CreateCaptchaChallenge(ctx context.Context, id, username, remoteIP, answerHash string, expiresAt, now time.Time) error {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM captcha_challenges WHERE julianday(expires_at)<=julianday(?) OR (username=? AND remote_ip=?)`, formatTime(now), strings.ToLower(strings.TrimSpace(username)), remoteIP)
	_, err := s.db.ExecContext(ctx, `INSERT INTO captcha_challenges(id,username,remote_ip,answer_hash,expires_at,created_at) VALUES(?,?,?,?,?,?)`, id, strings.ToLower(strings.TrimSpace(username)), remoteIP, answerHash, formatTime(expiresAt), formatTime(now))
	return err
}

func (s *Store) ConsumeCaptchaChallenge(ctx context.Context, id, username, remoteIP, answerHash string, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var stored string
	err = tx.QueryRowContext(ctx, `SELECT answer_hash FROM captcha_challenges WHERE id=? AND username=? AND remote_ip=? AND julianday(expires_at)>julianday(?)`, id, strings.ToLower(strings.TrimSpace(username)), remoteIP, formatTime(now)).Scan(&stored)
	_, _ = tx.ExecContext(ctx, `DELETE FROM captcha_challenges WHERE id=? AND username=? AND remote_ip=?`, id, strings.ToLower(strings.TrimSpace(username)), remoteIP)
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && stored == answerHash, err
}

func (s *Store) ListAudit(ctx context.Context, limit int, beforeID int64) ([]AuditEntry, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	query := `SELECT a.id,a.user_id,COALESCE(u.username,''),a.action,a.object_type,a.object_id,a.remote_ip,a.details_json,a.created_at FROM audit_log a LEFT JOIN users u ON u.id=a.user_id`
	args := []any{}
	if beforeID > 0 {
		query += ` WHERE a.id<?`
		args = append(args, beforeID)
	}
	query += ` ORDER BY a.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AuditEntry, 0)
	for rows.Next() {
		var item AuditEntry
		var userID sql.NullString
		var details, created string
		if err := rows.Scan(&item.ID, &userID, &item.Username, &item.Action, &item.ObjectType, &item.ObjectID, &item.RemoteIP, &details, &created); err != nil {
			return nil, err
		}
		if userID.Valid {
			value := userID.String
			item.UserID = &value
		}
		item.Details = []byte(details)
		item.CreatedAt, _ = parseTime(created)
		items = append(items, item)
	}
	return items, rows.Err()
}
