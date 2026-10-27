package agentgateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Dedicated opt-in roots retain failed synthetic files for the test owner.
func networkFixtureDir(t testing.TB) string {
	t.Helper()
	root := os.Getenv("MYPROBE_NETWORK_TEST_DIR")
	if root == "" {
		return t.TempDir()
	}
	if !filepath.IsAbs(root) {
		t.Fatal("MYPROBE_NETWORK_TEST_DIR must be absolute")
	}
	name := strings.NewReplacer("/", "_", "\\", "_").Replace(t.Name())
	dir, err := os.MkdirTemp(root, "gateway-"+name+"-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
