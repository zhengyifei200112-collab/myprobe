package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Retain source DB/WAL/SHM and output artifacts on failed opt-in regressions.
// The established HTTP fixture root is also preserved by the CI failure upload.
func backupFixtureDir(t testing.TB) string {
	t.Helper()
	root := os.Getenv("MYPROBE_BACKUP_TEST_DIR")
	if root == "" {
		root = os.Getenv("MYPROBE_HTTP_TEST_DIR")
	}
	if root == "" {
		return t.TempDir()
	}
	if !filepath.IsAbs(root) {
		t.Fatal("backup fixture root must be an absolute dedicated directory")
	}
	dir, err := os.MkdirTemp(root, "backup-"+strings.ReplaceAll(t.Name(), "/", "_")+"-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
