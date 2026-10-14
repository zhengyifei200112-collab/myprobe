package notificationlink

import (
	"net/url"
	"testing"
	"time"
)

func TestServiceDiagnosticContextPrivacyAndBounds(t *testing.T) {
	at := time.Date(2026, 10, 13, 12, 0, 0, 1, time.FixedZone("offset", 8*3600))
	for _, origin := range []string{"", "https://notify.example.test"} {
		path := ServiceDiagnosticAt(origin, "service-id", "observer-id", at)
		u, err := url.Parse(path)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		start, e1 := time.Parse(time.RFC3339Nano, q.Get("start"))
		end, e2 := time.Parse(time.RFC3339Nano, q.Get("end"))
		if e1 != nil || e2 != nil || !start.Equal(at.Add(-30*time.Minute)) || !end.Equal(at.Add(5*time.Minute)) || q.Get("service") != "service-id" || q.Get("observer") != "observer-id" || len(q) != 4 || u.Path != "/admin" {
			t.Fatal("diagnostic context")
		}
	}
	for _, id := range []string{"", "//private.test", "a b", "x\n", "https://target.test"} {
		if ServiceDiagnosticAt("", id, "node", at) != "" {
			t.Fatal("unsafe ID")
		}
	}
	if ServiceDiagnostic("", "service", "node", at, at.Add(31*24*time.Hour)) == "" || ServiceDiagnostic("", "service", "node", at, at.Add(31*24*time.Hour+time.Nanosecond)) != "" || ServiceDiagnostic("", "service", "node", at, at) != "" || ServiceDiagnosticAt("", "service", "node", time.Time{}) != "" {
		t.Fatal("diagnostic range bounds")
	}
}
