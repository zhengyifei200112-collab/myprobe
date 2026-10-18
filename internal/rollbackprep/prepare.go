// Package rollbackprep prepares a new copy of a closed legacy backup. It never
// opens the source for writing or migrates application tables to a newer layout.
package rollbackprep

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/001_history_epoch_lookup.sql
var migration string

const MigrationName = "001_history_epoch_lookup.sql"
const LegacyVersion = "013_github_oauth.sql"

var ErrInvalidInput = errors.New("a closed legacy backup, new destination and expected SHA-256 are required")
var ErrSourceChanged = errors.New("source snapshot differs from the expected SHA-256")
var ErrUnsupportedSchema = errors.New("rollback preparation supports only the schema-013 table layout")
var ErrExistingIndex = errors.New("an existing rollback index has a different definition")

type Result struct {
	Format          string   `json:"format"`
	Migration       string   `json:"migration"`
	MigrationSHA256 string   `json:"migration_sha256"`
	SourceSHA256    string   `json:"source_sha256"`
	PreparedSHA256  string   `json:"prepared_sha256"`
	ContentSHA256   string   `json:"content_sha256"`
	SchemaVersion   string   `json:"schema_version"`
	SourceBytes     int64    `json:"source_bytes"`
	PreparedBytes   int64    `json:"prepared_bytes"`
	Rows            int64    `json:"rows"`
	Reports         int64    `json:"reports"`
	Tables          int      `json:"tables"`
	Indexes         []string `json:"indexes"`
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

// Prepare publishes destination only after every check succeeds. Publication is
// an exclusive hard link in its own directory: an existing file is never replaced.
func Prepare(ctx context.Context, source, destination, expectedSHA256 string) (result Result, err error) {
	decoded, e := hex.DecodeString(expectedSHA256)
	if e != nil || len(decoded) != sha256.Size || !filepath.IsAbs(source) || !filepath.IsAbs(destination) {
		return result, ErrInvalidInput
	}
	info, e := os.Lstat(source)
	if e != nil {
		return result, e
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return result, ErrInvalidInput
	}
	for _, suffix := range []string{"-wal", "-journal"} {
		if f, e := os.Stat(source + suffix); e == nil && f.Size() > 0 {
			return result, ErrInvalidInput
		} else if e != nil && !errors.Is(e, os.ErrNotExist) {
			return result, e
		}
	}
	if _, e = os.Lstat(destination); e == nil {
		return result, os.ErrExist
	} else if !errors.Is(e, os.ErrNotExist) {
		return result, e
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(destination))
	if e != nil {
		return result, e
	}
	destination = filepath.Join(parent, filepath.Base(destination))
	input, e := os.Open(source)
	if e != nil {
		return result, e
	}
	defer input.Close()
	opened, e := input.Stat()
	if e != nil {
		return result, e
	}
	if !os.SameFile(info, opened) {
		return result, ErrSourceChanged
	}
	tmp, e := os.CreateTemp(parent, ".myprobe-rollback-*")
	if e != nil {
		return result, e
	}
	temporary := tmp.Name()
	identity, e := tmp.Stat()
	if e != nil {
		tmp.Close()
		return result, e
	}
	defer func() {
		tmp.Close()
		if current, e := os.Lstat(temporary); e == nil && os.SameFile(identity, current) {
			_ = os.Remove(temporary)
		}
	}()
	h := sha256.New()
	n, e := io.CopyBuffer(io.MultiWriter(tmp, h), contextReader{ctx, input}, make([]byte, 1<<20))
	if e != nil {
		return result, e
	}
	if e = tmp.Sync(); e != nil {
		return result, e
	}
	if e = tmp.Close(); e != nil {
		return result, e
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(actual, expectedSHA256) || n != info.Size() {
		return result, ErrSourceChanged
	}
	if after, e := input.Stat(); e != nil {
		return result, e
	} else if after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return result, ErrSourceChanged
	}
	db, e := sql.Open("sqlite", "file:"+url.PathEscape(filepath.ToSlash(temporary))+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if e != nil {
		return result, e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var version string
	if e = db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),'') FROM schema_migrations").Scan(&version); e != nil {
		return result, ErrUnsupportedSchema
	}
	if version != LegacyVersion {
		return result, ErrUnsupportedSchema
	}
	if e = integrity(ctx, db); e != nil {
		return result, e
	}
	before, e := fingerprint(ctx, db)
	if e != nil {
		return result, e
	}
	indexNames := []string{"idx_metric_rollups_epoch_lookup", "idx_latency_rollups_epoch_lookup", "idx_traffic_rollups_epoch_lookup"}
	definitions := []string{"CREATE INDEX idx_metric_rollups_epoch_lookup ON metric_rollups(node_id,bucket_seconds,unixepoch(bucket_at))", "CREATE INDEX idx_latency_rollups_epoch_lookup ON latency_rollups(node_id,target_id,kind,bucket_seconds,unixepoch(bucket_at))", "CREATE INDEX idx_traffic_rollups_epoch_lookup ON traffic_rollups(node_id,bucket_seconds,unixepoch(bucket_at))"}
	for i, name := range indexNames {
		var existing string
		e = db.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type='index' AND name=?", name).Scan(&existing)
		if e == nil && canonicalSQL(existing) != canonicalSQL(definitions[i]) {
			return result, ErrExistingIndex
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return result, e
		}
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return result, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, migration); e != nil {
		return result, e
	}
	if e = tx.Commit(); e != nil {
		return result, e
	}
	after, e := fingerprint(ctx, db)
	if e != nil {
		return result, e
	}
	if before != after {
		return result, errors.New("rollback preparation changed stored content or table definitions")
	}
	if e = integrity(ctx, db); e != nil {
		return result, e
	}
	var journal string
	if e = db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); e != nil {
		return result, e
	}
	if journal == "wal" {
		var busy, frames, checkpointed int
		if e = db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &checkpointed); e != nil {
			return result, e
		}
		if busy != 0 || frames != checkpointed {
			return result, errors.New("prepared backup checkpoint incomplete")
		}
	}
	if e = db.Close(); e != nil {
		return result, e
	}
	prepared, e := fileSHA(ctx, temporary)
	if e != nil {
		return result, e
	}
	if e = ctx.Err(); e != nil {
		return result, e
	}
	final, e := os.OpenFile(temporary, os.O_RDWR, 0)
	if e != nil {
		return result, e
	}
	e = final.Sync()
	finalInfo, statErr := final.Stat()
	closeErr := final.Close()
	if e != nil {
		return result, e
	}
	if statErr != nil {
		return result, statErr
	}
	if closeErr != nil {
		return result, closeErr
	}
	if e = os.Link(temporary, destination); e != nil {
		return result, fmt.Errorf("publish prepared snapshot without replacement: %w", e)
	}
	migrationHash := sha256.Sum256([]byte(migration))
	return Result{Format: "myprobe.legacy-rollback-preparation.v1", Migration: MigrationName, MigrationSHA256: hex.EncodeToString(migrationHash[:]), SourceSHA256: actual, PreparedSHA256: prepared, ContentSHA256: before.Hash, SchemaVersion: version, SourceBytes: n, PreparedBytes: finalInfo.Size(), Rows: before.Rows, Reports: before.Reports, Tables: before.Tables, Indexes: indexNames}, nil
}

func canonicalSQL(s string) string {
	s = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), ";"))
	s = strings.ReplaceAll(s, "if not exists ", "")
	return strings.Join(strings.Fields(s), "")
}
func integrity(ctx context.Context, db *sql.DB) error {
	var v string
	if e := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&v); e != nil {
		return e
	}
	if v != "ok" {
		return errors.New("backup integrity check failed")
	}
	return nil
}
func fileSHA(ctx context.Context, p string) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, contextReader{ctx, f}); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type content struct {
	Hash          string
	Rows, Reports int64
	Tables        int
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func hashBytes(h hash.Hash, tag byte, b []byte) {
	_, _ = h.Write([]byte{tag})
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(b)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(b)
}
func hashValue(h hash.Hash, v any) error {
	var b [8]byte
	switch v := v.(type) {
	case nil:
		hashBytes(h, 'n', nil)
	case int64:
		binary.BigEndian.PutUint64(b[:], uint64(v))
		hashBytes(h, 'i', b[:])
	case float64:
		binary.BigEndian.PutUint64(b[:], math.Float64bits(v))
		hashBytes(h, 'f', b[:])
	case string:
		hashBytes(h, 's', []byte(v))
	case []byte:
		hashBytes(h, 'b', v)
	default:
		return errors.New("unsupported SQLite value type")
	}
	return nil
}
func fingerprint(ctx context.Context, db *sql.DB) (out content, err error) {
	h := sha256.New()
	rows, e := db.QueryContext(ctx, "SELECT type,name,sql FROM sqlite_master WHERE type IN ('table','trigger','view') ORDER BY type,name")
	if e != nil {
		return out, e
	}
	var tables []string
	for rows.Next() {
		var kind, name string
		var sqlText sql.NullString
		if e = rows.Scan(&kind, &name, &sqlText); e != nil {
			rows.Close()
			return out, e
		}
		hashBytes(h, 'o', []byte(kind))
		hashBytes(h, 't', []byte(name))
		hashBytes(h, 'd', []byte(sqlText.String))
		if kind == "table" {
			tables = append(tables, name)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	for _, name := range tables {
		hashBytes(h, 'T', []byte(name))
		data, e := db.QueryContext(ctx, "SELECT rowid,* FROM "+quote(name)+" ORDER BY rowid")
		if e != nil {
			return out, e
		}
		columns, e := data.Columns()
		if e != nil {
			data.Close()
			return out, e
		}
		for _, col := range columns {
			hashBytes(h, 'c', []byte(col))
		}
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		for data.Next() {
			if e = data.Scan(pointers...); e != nil {
				data.Close()
				return out, e
			}
			hashBytes(h, 'r', nil)
			for _, v := range values {
				if e = hashValue(h, v); e != nil {
					data.Close()
					return out, e
				}
			}
			out.Rows++
			if name == "metric_samples" {
				out.Reports++
			}
		}
		e = data.Err()
		data.Close()
		if e != nil {
			return out, e
		}
		out.Tables++
	}
	out.Hash = hex.EncodeToString(h.Sum(nil))
	return out, nil
}
