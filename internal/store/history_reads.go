package store

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
)

// OpenOptions keeps an operational escape hatch for SQLite's mapped-I/O error
// behavior. It changes read connections only, never the database format.
type OpenOptions struct {
	DisableHistoryMmap bool
}

const historyReadConnections = 2
const historyReadMmapBytes int64 = 2147418112

func openHistoryReadDB(path string, options OpenOptions) (*sql.DB, error) {
	if path == ":memory:" {
		// Keep this Store's existing unique shared-memory database and pool.
		return nil, nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := "file:" + url.PathEscape(filepath.ToSlash(absolute)) +
		"?mode=ro&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=query_only(1)"
	if !options.DisableHistoryMmap && runtime.GOOS == "linux" && strconv.IntSize == 64 {
		dsn += "&_pragma=mmap_size(" + strconv.FormatInt(historyReadMmapBytes, 10) + ")"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(historyReadConnections)
	// Closing Rows releases its connection instead of retaining mappings, cached
	// pages, statements or read transactions between requests. The writer pool
	// stays open, so closing a history reader is not the last writer checkpoint.
	db.SetMaxIdleConns(0)
	return db, nil
}

func (s *Store) historyQueryDB() *sql.DB {
	if s.historyDB != nil {
		return s.historyDB
	}
	// In-memory Stores and migration/diagnostic fixtures that own only db keep
	// the same connection and visibility semantics as before.
	return s.db
}
