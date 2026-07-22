package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrRestorePending = errors.New("a database restore is already pending")

func (s *Store) ConsistentBackup(ctx context.Context, destination string) error {
	if destination == "" {
		return errors.New("backup destination is required")
	}
	if _, err := os.Stat(destination); err == nil {
		return errors.New("backup destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	quoted := strings.ReplaceAll(filepath.ToSlash(destination), "'", "''")
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO '"+quoted+"'"); err != nil {
		return fmt.Errorf("create consistent SQLite backup: %w", err)
	}
	return nil
}

func ValidateDatabaseFile(ctx context.Context, path string) error {
	if path == "" {
		return errors.New("database path is required")
	}
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(filepath.ToSlash(path))+"?mode=ro&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("check backup integrity: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("backup integrity check failed: %s", integrity)
	}
	for _, table := range []string{"nodes", "users", "schema_migrations"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			return fmt.Errorf("backup is missing required table %s", table)
		}
	}
	return nil
}

func StageDatabaseRestore(ctx context.Context, databasePath, validatedDatabasePath string) (string, error) {
	if databasePath == "" || databasePath == ":memory:" {
		return "", errors.New("database restore requires a file-backed SQLite database")
	}
	if err := ValidateDatabaseFile(ctx, validatedDatabasePath); err != nil {
		return "", err
	}
	pending := databasePath + ".restore"
	if _, err := os.Stat(pending); err == nil {
		return "", ErrRestorePending
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.Rename(validatedDatabasePath, pending); err != nil {
		return "", fmt.Errorf("stage database restore: %w", err)
	}
	return pending, nil
}

func ApplyPendingRestore(ctx context.Context, databasePath string, now time.Time) (string, error) {
	if databasePath == "" || databasePath == ":memory:" {
		return "", nil
	}
	pending := databasePath + ".restore"
	if _, err := os.Stat(pending); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	if err := ValidateDatabaseFile(ctx, pending); err != nil {
		return "", fmt.Errorf("validate pending restore: %w", err)
	}
	suffix := ".pre-restore-" + now.UTC().Format("20060102T150405Z")
	recovery := databasePath + suffix
	hadCurrent := false
	if _, err := os.Stat(databasePath); err == nil {
		hadCurrent = true
		if _, err := os.Stat(recovery); err == nil {
			return "", errors.New("recovery database path already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err := os.Rename(databasePath, recovery); err != nil {
			return "", fmt.Errorf("preserve current database: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	movedSidecars := make([]string, 0, 2)
	for _, sidecar := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(databasePath + sidecar); err == nil {
			if _, err := os.Stat(recovery + sidecar); err == nil {
				for _, moved := range movedSidecars {
					_ = os.Rename(recovery+moved, databasePath+moved)
				}
				if hadCurrent {
					_ = os.Rename(recovery, databasePath)
				}
				return "", fmt.Errorf("recovery database sidecar path already exists: %s", recovery+sidecar)
			} else if !errors.Is(err, os.ErrNotExist) {
				for _, moved := range movedSidecars {
					_ = os.Rename(recovery+moved, databasePath+moved)
				}
				if hadCurrent {
					_ = os.Rename(recovery, databasePath)
				}
				return "", err
			}
			if err := os.Rename(databasePath+sidecar, recovery+sidecar); err != nil {
				for _, moved := range movedSidecars {
					_ = os.Rename(recovery+moved, databasePath+moved)
				}
				if hadCurrent {
					_ = os.Rename(recovery, databasePath)
				}
				return "", fmt.Errorf("preserve current database sidecar %s: %w", sidecar, err)
			}
			movedSidecars = append(movedSidecars, sidecar)
		} else if !errors.Is(err, os.ErrNotExist) {
			for _, moved := range movedSidecars {
				_ = os.Rename(recovery+moved, databasePath+moved)
			}
			if hadCurrent {
				_ = os.Rename(recovery, databasePath)
			}
			return "", err
		}
	}
	if err := os.Rename(pending, databasePath); err != nil {
		for _, moved := range movedSidecars {
			_ = os.Rename(recovery+moved, databasePath+moved)
		}
		if hadCurrent {
			_ = os.Rename(recovery, databasePath)
		}
		return "", fmt.Errorf("activate pending restore: %w", err)
	}
	if !hadCurrent {
		recovery = ""
	}
	return recovery, nil
}
