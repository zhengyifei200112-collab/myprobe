package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

type upgradeTableEvidence struct {
	Columns []string
	Rows    int64
	Digest  string
}

type offlineUpgradeResult struct {
	Format             string           `json:"format"`
	SourceCommit       string           `json:"source_commit"`
	OS                 string           `json:"os"`
	Arch               string           `json:"arch"`
	GoVersion          string           `json:"go_version"`
	ExecutableSHA256   string           `json:"executable_sha256"`
	InputSHA256        string           `json:"input_sha256"`
	InputBytes         int64            `json:"input_bytes"`
	OriginalMigrations []string         `json:"original_migrations"`
	UpgradedMigrations []string         `json:"upgraded_migrations"`
	PreservedTableRows map[string]int64 `json:"preserved_table_rows"`
	Checks             []string         `json:"checks"`
	ElapsedSeconds     float64          `json:"elapsed_seconds"`
	Scope              string           `json:"scope"`
}

type rehearsalReader struct {
	ctx context.Context
	io.Reader
}

func (r rehearsalReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func rehearsalFileHash(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, rehearsalReader{ctx, f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func rehearsalCopy(ctx context.Context, source, destination string) error {
	a, err := os.Open(source)
	if err != nil {
		return err
	}
	defer a.Close()
	b, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(b, rehearsalReader{ctx, a})
	syncErr := b.Sync()
	closeErr := b.Close()
	return errors.Join(copyErr, syncErr, closeErr)
}

func rehearsalReadOnly(path string) (*sql.DB, error) {
	// This helper accepts only a closed, hash-verified backup, never a live WAL
	// database. immutable+mode=ro forbids even source-side journal/shm creation.
	return sql.Open("sqlite", "file:"+url.PathEscape(filepath.ToSlash(path))+"?mode=ro&immutable=1&_pragma=query_only(1)")
}

func rehearsalMigrations(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result = append(result, name)
	}
	return result, rows.Err()
}

func rehearsalTable(ctx context.Context, db *sql.DB, table string, columns []string) (upgradeTableEvidence, error) {
	// Only the fixed table allowlist below calls this function. Quote discovered
	// legacy column identifiers, preserving their values without logging any row.
	result := upgradeTableEvidence{Columns: columns}
	if columns == nil {
		rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
		if err != nil {
			return result, err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return result, err
			}
			result.Columns = append(result.Columns, name)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return result, err
		}
	}
	if len(result.Columns) == 0 {
		return result, errors.New("missing legacy table columns")
	}
	quoted := make([]string, len(result.Columns))
	for i, c := range result.Columns {
		quoted[i] = `"` + strings.ReplaceAll(c, `"`, `""`) + `"`
	}
	rows, err := db.QueryContext(ctx, `SELECT `+strings.Join(quoted, ",")+` FROM "`+table+`" ORDER BY id`)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	h := sha256.New()
	encoder := json.NewEncoder(h)
	for rows.Next() {
		values := make([]any, len(quoted))
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return result, err
		}
		if err := encoder.Encode(values); err != nil {
			return result, err
		}
		result.Rows++
	}
	result.Digest = hex.EncodeToString(h.Sum(nil))
	return result, rows.Err()
}

func rehearsalIntegrity(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var status string
		if err = rows.Scan(&status); err != nil || status != "ok" {
			rows.Close()
			return errors.New("database integrity check failed")
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil || count != 1 {
		return errors.New("database integrity check incomplete")
	}
	rows, err = db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("database foreign key check failed")
	}
	return rows.Err()
}

func rehearseOfflineUpgrade(ctx context.Context, source, output, expectedSHA, commit string) (offlineUpgradeResult, error) {
	t0 := time.Now()
	r := offlineUpgradeResult{Format: "myprobe.offline-upgrade-rehearsal.v1", SourceCommit: commit, OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version(), PreservedTableRows: map[string]int64{}, Scope: "closed-backup copy only; Store migrations/integrity/legacy-record preservation/reopen and old-schema restore copy; no Server startup, authentication, Provider, Agent, HTTP upload or production failover"}
	if decoded, err := hex.DecodeString(commit); err != nil || len(decoded) != 20 {
		return r, errors.New("declare the full frozen source commit and bind its executable separately")
	}
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return r, errors.New("input must be an existing regular closed backup")
	}
	r.InputBytes = info.Size()
	for _, suffix := range []string{"-wal", "-journal"} {
		if stat, err := os.Stat(source + suffix); err == nil && stat.Size() > 0 {
			return r, errors.New("input has a live journal; use a consistent closed backup")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return r, err
		}
	}
	dir, err := os.Lstat(output)
	if err != nil || !dir.IsDir() || runtime.GOOS != "windows" && dir.Mode().Perm()&0o077 != 0 {
		return r, errors.New("output must be an existing private directory")
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		return r, errors.New("output must be empty; existing files are never overwritten")
	}
	r.InputSHA256, err = rehearsalFileHash(ctx, source)
	if err != nil || len(expectedSHA) != 64 || r.InputSHA256 != expectedSHA {
		return r, errors.New("closed backup digest does not match operator manifest")
	}
	exe, err := os.Executable()
	if err != nil {
		return r, err
	}
	r.ExecutableSHA256, err = rehearsalFileHash(ctx, exe)
	if err != nil {
		return r, err
	}
	original, err := rehearsalReadOnly(source)
	if err != nil {
		return r, err
	}
	defer original.Close()
	r.OriginalMigrations, err = rehearsalMigrations(ctx, original)
	if err != nil {
		return r, err
	}
	before := map[string]upgradeTableEvidence{}
	for _, table := range []string{"users", "sessions", "nodes", "agent_tokens", "metric_samples", "audit_log"} {
		e, err := rehearsalTable(ctx, original, table, nil)
		if err != nil {
			return r, fmt.Errorf("read legacy %s: %w", table, err)
		}
		before[table] = e
		r.PreservedTableRows[table] = e.Rows
	}
	upgradedPath := filepath.Join(output, "upgraded.db")
	if err := rehearsalCopy(ctx, source, upgradedPath); err != nil {
		return r, err
	}
	upgraded, err := Open(ctx, upgradedPath)
	if err != nil {
		return r, err
	}
	defer upgraded.Close()
	r.UpgradedMigrations, err = rehearsalMigrations(ctx, upgraded.db)
	if err != nil {
		return r, err
	}
	if err := rehearsalIntegrity(ctx, upgraded.db); err != nil {
		return r, err
	}
	for table, expected := range before {
		actual, err := rehearsalTable(ctx, upgraded.db, table, expected.Columns)
		if err != nil || !reflect.DeepEqual(expected, actual) {
			return r, fmt.Errorf("legacy %s preservation check failed", table)
		}
	}
	if err := upgraded.Close(); err != nil {
		return r, err
	}
	reopened, err := Open(ctx, upgradedPath)
	if err != nil {
		return r, err
	}
	versions, err := rehearsalMigrations(ctx, reopened.db)
	reopened.Close()
	if err != nil || !reflect.DeepEqual(versions, r.UpgradedMigrations) {
		return r, errors.New("reopen changed migration receipts")
	}
	// Restore to a separate old-schema copy, never run the new Store against it.
	rollbackPath := filepath.Join(output, "old-schema-restore.db")
	if err := rehearsalCopy(ctx, source, rollbackPath); err != nil {
		return r, err
	}
	restored, err := rehearsalReadOnly(rollbackPath)
	if err != nil {
		return r, err
	}
	defer restored.Close()
	versions, err = rehearsalMigrations(ctx, restored)
	if err != nil || !reflect.DeepEqual(versions, r.OriginalMigrations) {
		return r, errors.New("old-schema restore receipt mismatch")
	}
	if err := rehearsalIntegrity(ctx, restored); err != nil {
		return r, err
	}
	for _, path := range []string{source, rollbackPath} {
		hash, err := rehearsalFileHash(ctx, path)
		if err != nil || hash != r.InputSHA256 {
			return r, errors.New("source changed or old-schema restore is not byte identical")
		}
	}
	r.Checks = []string{"input SHA-256 and closed-journal guard", "new private output and exclusive copies", "all current forward migrations", "upgraded integrity and foreign keys", "six legacy table counts and exact ordered-row digests", "idempotent migration reopen", "separate old-schema restore integrity", "old-schema receipt and file digest", "source backup digest unchanged"}
	r.ElapsedSeconds = time.Since(t0).Seconds()
	return r, nil
}

// TestOfflineUpgradeRehearsal is an operator opt-in. It never opens a production
// Server or invokes network clients/workers; only newly owned copies are migrated.
func TestOfflineUpgradeRehearsal(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_OFFLINE_UPGRADE") != "1" {
		t.Skip("set explicit closed-backup path, digest, source and empty private output")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	r, err := rehearseOfflineUpgrade(ctx, os.Getenv("MYPROBE_TEST_UPGRADE_BACKUP"), os.Getenv("MYPROBE_TEST_UPGRADE_OUTPUT"), os.Getenv("MYPROBE_TEST_UPGRADE_SHA256"), os.Getenv("MYPROBE_TEST_SOURCE_COMMIT"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(filepath.Join(os.Getenv("MYPROBE_TEST_UPGRADE_OUTPUT"), "result.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = out.Write(append(data, '\n'))
	err = errors.Join(err, out.Sync(), out.Close())
	if err != nil {
		t.Fatal(err)
	}
	t.Log("closed-backup rehearsal passed; screened result.json written privately")
}

func TestOfflineUpgradeRehearsalPreservesLegacyAndRejectsUnsafeInputs(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "legacy013.db")
	db, err := sql.Open("sqlite", sqliteDSN(source))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "014_" {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, entry.Name(), nowText()); err != nil {
			t.Fatal(err)
		}
	}
	old := &Store{db: db, path: source}
	if _, _, err := old.CreateNode(ctx, CreateNodeParams{Name: "private old node"}); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	hash, err := rehearsalFileHash(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	if err := os.Chmod(output, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := rehearseOfflineUpgrade(ctx, source, output, strings.Repeat("0", 64), strings.Repeat("a", 40)); err == nil {
		t.Fatal("digest mismatch accepted")
	}
	if err := os.WriteFile(source+"-wal", []byte("live journal"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rehearseOfflineUpgrade(ctx, source, output, hash, strings.Repeat("a", 40)); err == nil {
		t.Fatal("live source accepted")
	}
	if err := os.Remove(source + "-wal"); err != nil {
		t.Fatal(err)
	}
	r, err := rehearseOfflineUpgrade(ctx, source, output, hash, strings.Repeat("a", 40))
	if err != nil || len(r.OriginalMigrations) != 13 || len(r.Checks) != 9 || r.PreservedTableRows["nodes"] != 1 || r.PreservedTableRows["agent_tokens"] != 1 {
		t.Fatalf("synthetic rehearsal did not preserve records: %v", err)
	}
	if _, err := rehearseOfflineUpgrade(ctx, source, output, hash, strings.Repeat("a", 40)); err == nil {
		t.Fatal("populated output reused")
	}
}
