package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestRepositoryChangedFilesAndManifestCannotBePruned(t *testing.T) {
	r, db, dir := repositoryFixture(t, 1)
	ctx := context.Background()
	var generated []Artifact
	for i := 0; i < 3; i++ {
		a, err := r.Generate(ctx, db, "synthetic-backup-passphrase", time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		generated = append(generated, a)
	}
	if err := os.WriteFile(filepath.Join(dir, generated[0].FileName), []byte("changed ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, generated[1].FileName+".json"))
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"archive_verified":true`, `"archive_verified":false`, 1))
	if err = os.WriteFile(filepath.Join(dir, generated[1].FileName+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if removed, err := r.Prune(ctx); err != nil || removed != 0 {
		t.Fatalf("changed file pruned: %d %v", removed, err)
	}
	for _, a := range generated {
		if _, err = os.Stat(filepath.Join(dir, a.FileName)); err != nil {
			t.Fatal("changed or current file removed")
		}
	}
	if _, err = r.VerifyRecovery(ctx, generated[0].FileName, "synthetic-backup-passphrase", func(context.Context, *store.Store) error { return nil }); err == nil {
		t.Fatal("changed archive restored")
	}
}

func TestRepositoryCanonicalSourceAliases(t *testing.T) {
	r, db, dir := repositoryFixture(t, 1)
	ctx := context.Background()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	alias := strings.ToUpper(db.DatabasePath())
	if runtime.GOOS != "windows" {
		alias = filepath.Join(filepath.Dir(db.DatabasePath()), "source-alias.db")
		if err := os.Symlink(db.DatabasePath(), alias); err != nil {
			t.Skip("symlink fixture unavailable")
		}
	}
	same, err := OpenRepository(dir, alias, 1)
	if err != nil {
		t.Fatalf("canonical source alias rejected: %v", err)
	}
	defer same.Close()
	if _, err = same.Generate(ctx, db, "synthetic-backup-passphrase", time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryRetentionPreservesNewSuccessAfterClockRollback(t *testing.T) {
	r, db, _ := repositoryFixture(t, 1)
	ctx := context.Background()
	future := time.Now().Add(time.Hour)
	r.now = func() time.Time { return future }
	if _, err := r.Generate(ctx, db, "synthetic-backup-passphrase", future); err != nil {
		t.Fatal(err)
	}
	past := future.Add(-time.Hour)
	r.now = func() time.Time { return past }
	current, err := r.Generate(ctx, db, "synthetic-backup-passphrase", past)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := r.Prune(ctx, current.ID)
	if err != nil || removed != 1 {
		t.Fatalf("rollback retention: %d %v", removed, err)
	}
	remaining, err := r.Inventory(ctx)
	if err != nil || len(remaining) != 1 || remaining[0].ID != current.ID {
		t.Fatal("clock rollback deleted the newly completed backup")
	}
}

func TestRepositoryPublicationDoesNotOverwriteDestination(t *testing.T) {
	r, db, dir := repositoryFixture(t, 1)
	var collision string
	r.publish = func(old, new string) error {
		if strings.HasSuffix(new, ".mpb") {
			collision = new
			if err := os.WriteFile(filepath.Join(dir, new), []byte("foreign collision file"), 0o600); err != nil {
				return err
			}
		}
		return r.root.Link(old, new)
	}
	if _, err := r.Generate(context.Background(), db, "synthetic-backup-passphrase", time.Now()); err != RepositoryError("backup_publish_failed") {
		t.Fatalf("collision: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, collision))
	if err != nil || string(raw) != "foreign collision file" {
		t.Fatal("publication overwrote existing destination")
	}
}

func TestRepositoryRejectsSourceReuseAndSymlinkArtifact(t *testing.T) {
	r, db, dir := repositoryFixture(t, 1)
	ctx := context.Background()
	a, err := r.Generate(ctx, db, "synthetic-backup-passphrase", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	if other, err := OpenRepository(dir, filepath.Join(filepath.Dir(db.DatabasePath()), "other.db"), 1); err != RepositoryError("backup_directory_owner_mismatch") {
		if other != nil {
			other.Close()
		}
		t.Fatalf("source mismatch: %v", err)
	}
	reopened, err := OpenRepository(dir, db.DatabasePath(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	target := filepath.Join(t.TempDir(), "outside.mpb")
	raw, err := os.ReadFile(filepath.Join(dir, a.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(target, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(dir, a.FileName)); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(target, filepath.Join(dir, a.FileName)); err != nil {
		t.Skip("platform does not permit creating test symlinks")
	}
	items, err := reopened.Inventory(ctx)
	if err != nil || len(items) != 0 {
		t.Fatal("symlink included in managed inventory")
	}
	if _, err = reopened.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(target); err != nil {
		t.Fatal("outside target removed")
	}
	if _, err = os.Lstat(filepath.Join(dir, a.FileName)); errors.Is(err, os.ErrNotExist) {
		t.Fatal("symlink removed by retention")
	}
}
