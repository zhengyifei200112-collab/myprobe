package servicehealth

import (
	"encoding/hex"
	"strings"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

// CertificateDueDays returns only the most urgent currently reached threshold.
// Expired or not-yet-due certificates do not create expiry reminder intents.
func CertificateDueDays(notAfter, now time.Time) int {
	remaining := notAfter.Sub(now)
	if notAfter.IsZero() || remaining <= 0 {
		return 0
	}
	for _, days := range []int{1, 7, 14} {
		if remaining <= time.Duration(days)*24*time.Hour {
			return days
		}
	}
	return 0
}

func VerifiedCertificate(c httpcheck.Certificate, now time.Time) (httpcheck.Certificate, bool) {
	hash, err := hex.DecodeString(c.SHA256)
	if err != nil || len(hash) != 32 || !c.Verified || c.NotBefore.IsZero() || c.NotBefore.After(now) || !c.NotAfter.After(now) || !c.NotAfter.After(c.NotBefore) {
		return c, false
	}
	c.SHA256 = strings.ToLower(c.SHA256)
	c.NotBefore = c.NotBefore.UTC()
	c.NotAfter = c.NotAfter.UTC()
	return c, true
}
