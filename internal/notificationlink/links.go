package notificationlink

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
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
	return u.String(), nil
}
func Incident(origin, id string) string { return origin + "/admin?incident=" + url.QueryEscape(id) }
func Aggregate(origin, id, snapshot string) string {
	return origin + "/admin?aggregate=" + url.QueryEscape(id) + "&snapshot=" + url.QueryEscape(snapshot)
}
