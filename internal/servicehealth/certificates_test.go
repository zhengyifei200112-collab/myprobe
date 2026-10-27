package servicehealth

import (
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func TestCertificateThresholdsUseExactMostUrgentBoundary(t *testing.T) {
	now := time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC)
	for _, v := range []struct {
		remaining time.Duration
		want      int
	}{
		{14*24*time.Hour + time.Nanosecond, 0}, {14 * 24 * time.Hour, 14},
		{7*24*time.Hour + time.Nanosecond, 14}, {7 * 24 * time.Hour, 7},
		{24*time.Hour + time.Nanosecond, 7}, {24 * time.Hour, 1}, {time.Nanosecond, 1}, {0, 0}, {-time.Nanosecond, 0},
	} {
		if got := CertificateDueDays(now.Add(v.remaining), now); got != v.want {
			t.Fatalf("%s: got %d want %d", v.remaining, got, v.want)
		}
	}
	if CertificateDueDays(time.Time{}, now) != 0 {
		t.Fatal("zero expiry accepted")
	}
	c := httpcheck.Certificate{SHA256: strings.Repeat("AB", 32), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(14 * 24 * time.Hour), Verified: true}
	normalized, ok := VerifiedCertificate(c, now)
	if !ok || normalized.SHA256 != strings.ToLower(c.SHA256) {
		t.Fatal("fingerprint casing changed identity")
	}
	for _, mode := range []string{"unverified", "bad-hash", "expired", "not-yet-valid", "zero-start"} {
		value := c
		switch mode {
		case "unverified":
			value.Verified = false
		case "bad-hash":
			value.SHA256 = "private-marker"
		case "expired":
			value.NotAfter = now
		case "not-yet-valid":
			value.NotBefore = now.Add(time.Nanosecond)
		case "zero-start":
			value.NotBefore = time.Time{}
		}
		if _, ok := VerifiedCertificate(value, now); ok {
			t.Fatal("invalid certificate evidence accepted", mode)
		}
	}
}
