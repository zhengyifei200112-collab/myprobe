package backup

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

// A real separate process is killed while plaintext scratch and an unfinished
// output exist. No defer executes in the child; the next owner must recover.
func TestRepositoryKillHelper(t *testing.T) {
	if os.Getenv("MYPROBE_SYNTHETIC_BACKUP_KILL_HELPER") != "1" {
		return
	}
	db, err := store.Open(context.Background(), os.Getenv("MYPROBE_SYNTHETIC_BACKUP_SOURCE"))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := OpenRepository(os.Getenv("MYPROBE_SYNTHETIC_BACKUP_DIRECTORY"), db.DatabasePath(), 1)
	if err != nil {
		t.Fatal(err)
	}
	ready := os.Getenv("MYPROBE_SYNTHETIC_BACKUP_READY")
	stop := func() {
		if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
			t.Fatal(err)
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	if os.Getenv("MYPROBE_SYNTHETIC_BACKUP_KILL_PHASE") == "encrypt" {
		repo.encrypt = func(w io.Writer, _ io.Reader, _ string) error {
			if _, err := w.Write([]byte("synthetic interrupted ciphertext")); err != nil {
				return err
			}
			stop()
			return nil
		}
	} else {
		repo.publish = func(old, new string) error {
			err := repo.root.Link(old, new)
			if err == nil && strings.HasSuffix(new, ".mpb") {
				stop()
			}
			return err
		}
	}
	if _, err = repo.Generate(context.Background(), db, "synthetic-backup-passphrase", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	t.Fatal("kill helper returned without interruption")
}
func TestRepositoryRealProcessKillPreservesLastGoodAndCleansPlaintext(t *testing.T) {
	for _, phase := range []string{"encrypt", "publish"} {
		t.Run(phase, func(t *testing.T) {
			repo, db, dir := repositoryFixture(t, 1)
			ctx := context.Background()
			good, err := repo.Generate(ctx, db, "synthetic-backup-passphrase", time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if err = repo.Close(); err != nil {
				t.Fatal(err)
			}
			ready := filepath.Join(t.TempDir(), "helper-ready")
			cmd := exec.Command(os.Args[0], "-test.run=^TestRepositoryKillHelper$", "-test.timeout=30s")
			cmd.Env = append(os.Environ(), "MYPROBE_SYNTHETIC_BACKUP_KILL_HELPER=1", "MYPROBE_SYNTHETIC_BACKUP_SOURCE="+db.DatabasePath(), "MYPROBE_SYNTHETIC_BACKUP_DIRECTORY="+dir, "MYPROBE_SYNTHETIC_BACKUP_READY="+ready, "MYPROBE_SYNTHETIC_BACKUP_KILL_PHASE="+phase)
			var output bytes.Buffer
			cmd.Stdout = &output
			cmd.Stderr = &output
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			deadline := time.Now().Add(15 * time.Second)
			for {
				if _, err = os.Stat(ready); err == nil {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("helper exited early: %v %s", err, output.String())
				default:
				}
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					<-done
					t.Fatal("helper did not reach injection point")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err = cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err = <-done; err == nil {
				t.Fatal("child did not terminate abnormally")
			}
			next, err := OpenRepository(dir, db.DatabasePath(), 1)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			artifacts, err := next.Inventory(ctx)
			if err != nil || len(artifacts) != 1 || artifacts[0].ID != good.ID {
				t.Fatalf("last good changed: %+v %v", artifacts, err)
			}
			entries, err := os.ReadDir(filepath.Dir(db.DatabasePath()))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if scratchName.MatchString(entry.Name()) {
					t.Fatal("owned plaintext scratch survived reconciliation")
				}
			}
		})
	}
}
