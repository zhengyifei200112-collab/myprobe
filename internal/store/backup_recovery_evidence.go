package store

import (
	"context"
	"errors"
	"time"
)

type BackupRecoveryEvidence struct {
	Nodes    int64 `json:"nodes"`
	Users    int64 `json:"users"`
	Reports  int64 `json:"reports"`
	Channels int64 `json:"channels"`
}

// ReadRecoveredState exercises application tables in the isolated restored
// database and checks all SQLite foreign keys. It does not serve traffic or
// resume restored tasks/notifications.
func (s *Store) ReadRecoveredState(ctx context.Context) (BackupRecoveryEvidence, error) {
	var evidence BackupRecoveryEvidence
	rows, err := s.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return evidence, err
	}
	violated := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil {
		return evidence, err
	}
	if violated {
		return evidence, errors.New("restored_foreign_key_failure")
	}
	for _, item := range []struct {
		query string
		count *int64
	}{
		{`SELECT COUNT(*) FROM nodes`, &evidence.Nodes},
		{`SELECT COUNT(*) FROM users`, &evidence.Users},
		{`SELECT COUNT(*) FROM metric_samples`, &evidence.Reports},
		{`SELECT COUNT(*) FROM notification_channels`, &evidence.Channels},
	} {
		if err = s.db.QueryRowContext(ctx, item.query).Scan(item.count); err != nil {
			return evidence, err
		}
	}
	if _, err = s.GetSiteSettings(ctx); err != nil {
		return evidence, err
	}
	if _, err = s.ExportConfig(ctx, time.Now().UTC()); err != nil {
		return evidence, err
	}
	return evidence, nil
}
