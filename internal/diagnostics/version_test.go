package diagnostics

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionObservationOmitsPrivateLabels(t *testing.T) {
	private := "synthetic-private-key-203.0.113.9"
	good := ObserveVersion("v1.2.3-" + private + "+private-build")
	if good.Status != "release" || good.Base != "1.2.3" || !good.Prerelease || !good.BuildMetadata {
		t.Fatal("release structure not preserved")
	}
	raw, _ := json.Marshal(good)
	if strings.Contains(string(raw), private) || strings.Contains(string(raw), "private-build") {
		t.Fatal("private label disclosed")
	}
	for _, input := range []string{private, "https://example.invalid/private", "203.0.113.9", strings.Repeat("x", 129), "01.2.3"} {
		observation := ObserveVersion(input)
		if observation.Status != "unavailable" || observation.Base != "" {
			t.Fatalf("unrecognized private version retained: %q", input)
		}
	}
}

func TestVersionObservationCanonicalizesPrefix(t *testing.T) {
	if ObserveVersion("1.2.3") != ObserveVersion("v1.2.3") {
		t.Fatal("same release was split by optional version prefix")
	}
}
