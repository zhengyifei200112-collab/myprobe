package collector

import "testing"

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
