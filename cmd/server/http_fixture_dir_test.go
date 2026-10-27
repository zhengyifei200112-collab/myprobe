package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Keep local synthetic HTTP fixtures on failure when a dedicated directory is set.
func httpFixtureDir(t testing.TB) string {
	t.Helper()
	root := os.Getenv("MYPROBE_HTTP_TEST_DIR")
	if root == "" {
		return t.TempDir()
	}
	if !filepath.IsAbs(root) {
		t.Fatal("MYPROBE_HTTP_TEST_DIR must be an absolute dedicated directory")
	}
	dir, err := os.MkdirTemp(root, "server-"+strings.ReplaceAll(t.Name(), "/", "_")+"-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
