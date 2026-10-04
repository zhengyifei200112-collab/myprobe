// Package httpcheck defines bounded HTTP check data. It does not authorize a
// network connection; resolved addresses and redirects need executor policy.
package httpcheck

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const Capability = "http_probe.v1"

type Spec struct {
	URL          string `json:"url"`
	Method       string `json:"method"`
	StatusCodes  []int  `json:"status_codes"`
	TimeoutMS    int    `json:"timeout_ms"`
	MaxRedirects int    `json:"max_redirects"`
	MaxBodyBytes int    `json:"max_body_bytes"`
}

func (s Spec) Validate() error {
	if len(s.URL) == 0 || len(s.URL) > 2048 || !utf8.ValidString(s.URL) || strings.IndexFunc(s.URL, unicode.IsControl) >= 0 {
		return errors.New("invalid HTTP URL")
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Opaque != "" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.Contains(s.URL, "#") || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("HTTP URL must be absolute and omit credentials and fragments")
	}
	if port := u.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return errors.New("invalid HTTP URL port")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return errors.New("invalid HTTP URL port")
	}
	if s.Method != "GET" && s.Method != "HEAD" {
		return errors.New("HTTP method must be GET or HEAD")
	}
	if len(s.StatusCodes) == 0 || len(s.StatusCodes) > 32 {
		return errors.New("expected status codes must contain 1 to 32 entries")
	}
	seen := make(map[int]bool, len(s.StatusCodes))
	for _, status := range s.StatusCodes {
		if status < 100 || status > 599 || seen[status] {
			return errors.New("expected status codes must be unique HTTP codes")
		}
		seen[status] = true
	}
	if s.TimeoutMS < 100 || s.TimeoutMS > 60000 {
		return errors.New("HTTP timeout must be 100 to 60000 milliseconds")
	}
	if s.MaxRedirects < 0 || s.MaxRedirects > 3 {
		return errors.New("HTTP redirects must be between zero and three")
	}
	if s.MaxBodyBytes < 1 || s.MaxBodyBytes > 1<<20 {
		return errors.New("HTTP body limit must be 1 to 1048576 bytes")
	}
	return nil
}
