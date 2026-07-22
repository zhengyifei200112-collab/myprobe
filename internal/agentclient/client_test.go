package agentclient

import (
	"testing"
	"time"
)

func TestWithJitterStaysWithinDocumentedBounds(t *testing.T) {
	base := 10 * time.Second
	for range 1000 {
		value := withJitter(base)
		if value < 8*time.Second || value > 12*time.Second {
			t.Fatalf("jittered duration %s is outside 80%%-120%%", value)
		}
	}
}
