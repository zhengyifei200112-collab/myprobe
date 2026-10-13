package notificationlink

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Origins are operator configuration, never request Host/Forwarded headers.
func NormalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	invalid := errors.New("notification origin must be an HTTP(S) origin without credentials, path, query or fragment")
	if len(raw) > 512 || strings.IndexFunc(raw, unicode.IsSpace) >= 0 || strings.ContainsAny(raw, "?#") {
		return "", invalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Hostname() == "" || strings.HasSuffix(u.Host, ":") || (u.Scheme != "http" && u.Scheme != "https") || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return "", invalid
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", invalid
		}
	}
	u.Path = ""
	normalized := u.String()
	if len(normalized) > 512 {
		return "", invalid
	}
	return normalized, nil
}
func Incident(origin, id string) string { return origin + "/admin?incident=" + url.QueryEscape(id) }
func Aggregate(origin, id, snapshot string) string {
	return origin + "/admin?aggregate=" + url.QueryEscape(id) + "&snapshot=" + url.QueryEscape(snapshot)
}

var diagnosticID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:\-]{0,127}$`)

func ValidDiagnosticContext(serviceID, nodeID string, start, end time.Time) bool {
	return diagnosticID.MatchString(serviceID) && diagnosticID.MatchString(nodeID) && start.Year() >= 1970 && end.Year() <= 2100 && end.After(start) && end.Sub(start) <= 31*24*time.Hour
}

func ServiceDiagnostic(origin, serviceID, nodeID string, start, end time.Time) string {
	if !ValidDiagnosticContext(serviceID, nodeID, start, end) {
		return ""
	}
	q := url.Values{"service": {serviceID}, "observer": {nodeID}, "start": {start.UTC().Format(time.RFC3339Nano)}, "end": {end.UTC().Format(time.RFC3339Nano)}}
	return origin + "/admin?" + q.Encode()
}

// The context surrounds the notification time, not the precise outage duration.
func ServiceDiagnosticAt(origin, serviceID, nodeID string, at time.Time) string {
	return ServiceDiagnostic(origin, serviceID, nodeID, at.Add(-30*time.Minute), at.Add(5*time.Minute))
}
