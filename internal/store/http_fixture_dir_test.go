package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// httpFixtureDir can retain synthetic database files after a failed local run.
// With no opt-in directory, Go's ordinary test cleanup remains unchanged.
func httpFixtureDir(t testing.TB) string {
	t.Helper()
	root := os.Getenv("MYPROBE_HTTP_TEST_DIR")
	if root == "" {
		return t.TempDir()
	}
	if !filepath.IsAbs(root) {
		t.Fatal("MYPROBE_HTTP_TEST_DIR must be an absolute dedicated directory")
	}
	name := strings.ReplaceAll(t.Name(), "/", "_")
	dir, err := os.MkdirTemp(root, "store-"+name+"-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
