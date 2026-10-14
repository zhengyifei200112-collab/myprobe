//go:build linux

package backup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
	"golang.org/x/sys/unix"
)

// Opt-in real kernel ENOSPC. The supplied directory must be a disposable tmpfs
// capped at 256 MiB; the fixture only writes its own newly created child.
func TestRepositoryActualFilesystemENOSPCPreservesLastGood(t *testing.T) {
	mount := os.Getenv("MYPROBE_BACKUP_ENOSPC_DIRECTORY")
	if mount == "" {
		t.Skip("requires an isolated capped tmpfs")
	}
	if !filepath.IsAbs(mount) {
		t.Fatal("tmpfs path must be absolute")
	}
	var state unix.Statfs_t
	if err := unix.Statfs(mount, &state); err != nil {
		t.Fatal(err)
	}
	if state.Type != unix.TMPFS_MAGIC || state.Bsize <= 0 || state.Blocks > uint64(256<<20)/uint64(state.Bsize) {
		t.Fatal("requires a disposable tmpfs capped at 256 MiB")
	}
	dir, err := os.MkdirTemp(mount, "myprobe-enospc-")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != filepath.Clean(mount) {
		t.Fatal("fixture child escaped intended tmpfs")
	}
	defer os.RemoveAll(dir)
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo, err := OpenRepository(dir, db.DatabasePath(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	good, err := repo.Generate(ctx, db, "synthetic-backup-passphrase", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	observed := false
	repo.encrypt = func(w io.Writer, r io.Reader, pass string) error {
		filler, err := repo.root.OpenFile(".synthetic-enospc-fill", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		block := make([]byte, 512<<10)
		written := int64(0)
		for {
			n, e := filler.Write(block)
			written += int64(n)
			if e != nil {
				err = e
				break
			}
			if written > 256<<20 {
				filler.Close()
				return errors.New("capped filesystem limit was not enforced")
			}
		}
		filler.Close()
		if !errors.Is(err, syscall.ENOSPC) {
			return err
		}
		// Production preflight has already succeeded; another writer then exhausted
		// the volume. The actual archive writer must fail without a replacement.
		err = Encrypt(w, r, pass)
		observed = errors.Is(err, syscall.ENOSPC)
		return err
	}
	if _, err = repo.Generate(ctx, db, "synthetic-backup-passphrase", time.Now()); err != RepositoryError("backup_encryption_failed") || !observed {
		t.Fatalf("actual archive ENOSPC: observed=%v error=%v", observed, err)
	}
	items, err := repo.Inventory(ctx)
	if err != nil || len(items) != 1 || items[0].ID != good.ID {
		t.Fatal("filesystem ENOSPC changed last good artifact")
	}
}
