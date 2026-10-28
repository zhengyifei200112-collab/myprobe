package backup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func repositoryFixture(t *testing.T, keep int) (*Repository, *store.Store, string) {
	t.Helper()
	base := backupFixtureDir(t)
	s, err := store.Open(context.Background(), filepath.Join(base, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, _, err := s.CreateNode(context.Background(), store.CreateNodeParams{Name: "synthetic backup node"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "archives")
	r, err := OpenRepository(dir, s.DatabasePath(), keep)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, s, dir
}

func TestRepositoryAuthenticatedArchiveAndOwnedRetention(t *testing.T) {
	r, s, dir := repositoryFixture(t, 1)
	ctx := context.Background()
	var last Artifact
	for i := 0; i < 3; i++ {
		a, err := r.Generate(ctx, s, "synthetic-backup-passphrase", time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if !a.ArchiveVerified || a.RecoveryVerified || a.Bytes < 1 {
			t.Fatalf("invalid completion: %+v", a)
		}
		last = a
	}
	items, err := r.Inventory(ctx)
	if err != nil || len(items) != 3 {
		t.Fatalf("inventory: %d %v", len(items), err)
	}
	unowned := "myprobe-backup-20260101T000000Z-" + strings.Repeat("a", 32) + ".mpb"
	if err := os.WriteFile(filepath.Join(dir, unowned), []byte("unowned file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".myprobe-backup-part-foreign"), []byte("partial file"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := r.Prune(ctx)
	if err != nil || removed != 2 {
		t.Fatalf("cleanup: %d %v", removed, err)
	}
	items, err = r.Inventory(ctx)
	if err != nil || len(items) != 1 || items[0].ID != last.ID {
		t.Fatal("newest backup not retained")
	}
	for _, name := range []string{unowned, ".myprobe-backup-part-foreign"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal("unmanaged file removed")
		}
	}
	encoded, _ := json.Marshal(last)
	if strings.Contains(string(encoded), "synthetic-backup-passphrase") || strings.Contains(string(encoded), dir) || strings.Contains(string(encoded), r.owner.Key) {
		t.Fatal("completion metadata leaked secrets or paths")
	}
}

func TestRepositoryFailuresKeepLastGoodArtifact(t *testing.T) {
	r, s, _ := repositoryFixture(t, 1)
	ctx := context.Background()
	first, err := r.Generate(ctx, s, "synthetic-backup-passphrase", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	r.encrypt = func(w io.Writer, _ io.Reader, _ string) error {
		_, _ = w.Write([]byte("synthetic partial data"))
		return errors.New("synthetic disk full")
	}
	if _, err := r.Generate(ctx, s, "synthetic-backup-passphrase", time.Now().UTC()); err == nil {
		t.Fatal("partial write succeeded")
	}
	items, err := r.Inventory(ctx)
	if err != nil || len(items) != 1 || items[0].ID != first.ID {
		t.Fatal("failed generation replaced last good backup")
	}
	r.encrypt = Encrypt
	r.publish = func(string, string) error { return errors.New("synthetic atomic publish failure") }
	if _, err := r.Generate(ctx, s, "synthetic-backup-passphrase", time.Now().UTC()); err == nil {
		t.Fatal("failed publication succeeded")
	}
	items, err = r.Inventory(ctx)
	if err != nil || len(items) != 1 || items[0].ID != first.ID {
		t.Fatal("failed publication changed completed inventory")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.Generate(canceled, s, "synthetic-backup-passphrase", time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was not preserved")
	}
}
