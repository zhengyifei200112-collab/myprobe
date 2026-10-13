package notificationlink

import (
	"strings"
	"testing"
)

func TestOriginValidationAndCanonicalLinks(t *testing.T) {
	if _, err := NormalizeOrigin("https://" + strings.Repeat("界", 160)); err == nil {
		t.Fatal("encoded origin exceeded its stored bound")
	}
	for raw, want := range map[string]string{"": "", "  https://probe.example.com/  ": "https://probe.example.com", "http://127.0.0.1:25775": "http://127.0.0.1:25775", "https://[::1]:443/": "https://[::1]:443"} {
		got, err := NormalizeOrigin(raw)
		if err != nil || got != want {
			t.Errorf("origin %q: %q %v", raw, got, err)
		}
	}
	for _, raw := range []string{"//probe.example.com", "ftp://probe.example.com", "https://user:secret-marker@probe.example.com", "https://probe.example.com/admin", "https://probe.example.com/%2F", "https://probe.example.com?secret-marker", "https://probe.example.com#secret-marker", "https://probe.example.com?", "http://host:0", "http://host:65536", "http://host:", "http://host:bad", "https://", "https://ho st", "https://" + strings.Repeat("a", 505)} {
		got, err := NormalizeOrigin(raw)
		if err == nil || got != "" || strings.Contains(err.Error(), "secret-marker") {
			t.Errorf("invalid origin accepted or exposed: %q %v", got, err)
		}
	}
	if got := Incident("https://probe.example.com", "id:one&other=x"); got != "https://probe.example.com/admin?incident=id%3Aone%26other%3Dx" {
		t.Fatal(got)
	}
	if got := Aggregate("", "id:one", "hash"); got != "/admin?aggregate=id%3Aone&snapshot=hash" {
		t.Fatal(got)
	}
}
