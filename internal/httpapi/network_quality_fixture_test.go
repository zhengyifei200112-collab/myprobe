package httpapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Opt-in roots preserve failed synthetic bytes without changing normal TempDir
// behavior in ordinary local tests. Successful fixtures are retired by the runner.
func networkQualityAPIFixtureDir(t testing.TB) string {
	t.Helper()
	root := os.Getenv("MYPROBE_HTTP_TEST_DIR")
	if root == "" {
		return t.TempDir()
	}
	if !filepath.IsAbs(root) {
		t.Fatal("MYPROBE_HTTP_TEST_DIR must be an absolute dedicated directory")
	}
	dir, err := os.MkdirTemp(root, "httpapi-"+strings.ReplaceAll(t.Name(), "/", "_")+"-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
