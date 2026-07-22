package collector

import (
	"path/filepath"
	"testing"
)

func TestCleanUnique(t *testing.T) {
	got := cleanUnique([]string{" eth0 ", "eth0", "", "ens3"})
	if len(got) != 2 || got[0] != "eth0" || got[1] != "ens3" {
		t.Fatalf("cleanUnique() = %#v", got)
	}
}

func TestClampPercent(t *testing.T) {
	if clampPercent(-1) != 0 || clampPercent(101) != 100 || clampPercent(42) != 42 {
		t.Fatal("clampPercent did not constrain values")
	}
}

func TestDiskUsagePathUsesHostRootForAbsoluteMounts(t *testing.T) {
	hostRoot := filepath.Join(string(filepath.Separator), "host")
	mount := filepath.Join(string(filepath.Separator), "var", "lib")
	want := filepath.Join(hostRoot, "var", "lib")
	if got := diskUsagePath(hostRoot, mount); got != want {
		t.Fatalf("diskUsagePath() = %q, want %q", got, want)
	}
}

func TestDiskUsagePathPreservesLogicalPathsWithoutHostRoot(t *testing.T) {
	mount := filepath.Join(string(filepath.Separator), "var", "lib")
	if got := diskUsagePath("", mount); got != mount {
		t.Fatalf("diskUsagePath() = %q, want %q", got, mount)
	}
	if got := diskUsagePath("/host", "relative"); got != "relative" {
		t.Fatalf("diskUsagePath() changed relative mount to %q", got)
	}
	if got := diskUsagePath("relative", mount); got != mount {
		t.Fatalf("diskUsagePath() used relative host root: %q", got)
	}
}

func TestUpdateConfigPreservesHostRoot(t *testing.T) {
	hostRoot := filepath.Join(string(filepath.Separator), "host")
	collector := New(Config{HostRoot: hostRoot})
	collector.UpdateConfig(Config{Interfaces: []string{"eth0"}, Mounts: []string{"/"}})
	if collector.config.HostRoot != hostRoot {
		t.Fatalf("UpdateConfig() host root = %q, want %q", collector.config.HostRoot, hostRoot)
	}
}
