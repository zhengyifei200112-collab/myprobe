// Package releasecheck reads the fixed public upstream release endpoint. It
// neither downloads executables nor sends local versions, node data or tokens.
package releasecheck

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

const (
	Repository       = "zhengyifei200112-collab/myprobe"
	Endpoint         = "https://api.github.com/repos/" + Repository + "/releases/latest"
	DocumentationURL = "https://github.com/" + Repository + "/blob/main/docs/INSTALLATION.zh-CN.md"
	CheckInterval    = 24 * time.Hour
	MinimumInterval  = 15 * time.Minute
	FreshFor         = 48 * time.Hour
	maxBody          = 1 << 20
)

type Release struct {
	Tag          string    `json:"tag"`
	URL          string    `json:"url"`
	PublishedAt  time.Time `json:"published_at"`
	InstallerTag bool      `json:"installer_tag"`
}

type Snapshot struct {
	Status        string     `json:"status"`
	Source        string     `json:"source"`
	Automatic     bool       `json:"automatic"`
	Latest        *Release   `json:"latest"`
	CheckedAt     *time.Time `json:"checked_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	NextCheckAt   *time.Time `json:"next_check_at"`
	ErrorCode     string     `json:"error_code,omitempty"`
}

type Comparison struct {
	Status         string  `json:"status"`
	CurrentVersion *string `json:"current_version"`
	Scope          string  `json:"scope"`
}

type Checker struct {
	lifetime                   context.Context
	client                     *http.Client
	automatic                  bool
	now                        func() time.Time
	mu                         sync.Mutex
	flight                     chan struct{}
	latest                     *Release
	etag                       string
	checked, lastSuccess, next time.Time
	errorCode                  string
}

// A transport is injectable for owned fixtures; the endpoint, redirects,
// timeout and request headers remain controlled here, never by API input.
func New(ctx context.Context, automatic bool, transport http.RoundTripper) *Checker {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Checker{lifetime: ctx, automatic: automatic, now: time.Now, client: &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *Checker) Run() {
	if !c.automatic {
		return
	}
	c.Refresh(c.lifetime)
	timer := time.NewTicker(CheckInterval)
	defer timer.Stop()
	for {
		select {
		case <-c.lifetime.Done():
			return
		case <-timer.C:
			c.Refresh(c.lifetime)
		}
	}
}

// Concurrent callers join one bounded operation. Cancelling a browser request
// does not cancel another administrator's shared check or consume another slot.
func (c *Checker) Refresh(ctx context.Context) Snapshot {
	c.mu.Lock()
	flight := c.flight
	if flight == nil && c.lifetime.Err() == nil && !c.now().Before(c.next) {
		flight = make(chan struct{})
		c.flight = flight
		c.next = c.now().Add(MinimumInterval)
		etag := c.etag
		go c.fetch(flight, etag)
	}
	c.mu.Unlock()
	if flight != nil {
		select {
		case <-flight:
		case <-ctx.Done():
		}
	}
	return c.Snapshot()
}

func (c *Checker) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Snapshot{Status: "not_checked", Source: "github_public_release", Automatic: c.automatic}
	if c.latest != nil {
		copy := *c.latest
		s.Latest = &copy
	}
	utc := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		v := t.UTC()
		return &v
	}
	s.CheckedAt, s.LastSuccessAt, s.NextCheckAt = utc(c.checked), utc(c.lastSuccess), utc(c.next)
	s.ErrorCode = c.errorCode
	switch {
	case c.flight != nil:
		s.Status = "checking"
	case c.checked.IsZero():
	case c.latest == nil:
		s.Status = "unavailable"
	case c.errorCode != "" || c.now().Sub(c.lastSuccess) > FreshFor || c.now().Before(c.lastSuccess):
		s.Status = "stale"
	default:
		s.Status = "available"
	}
	return s
}

func (c *Checker) fetch(done chan struct{}, etag string) {
	ctx, cancel := context.WithTimeout(c.lifetime, 5*time.Second)
	defer cancel()
	release, newETag, code, retry := c.request(ctx, etag)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checked = c.now()
	c.errorCode = code
	if code == "" {
		if release != nil {
			c.latest = release
			c.etag = newETag
		}
		if c.latest == nil {
			c.errorCode = "invalid_response"
		} else {
			c.lastSuccess = c.checked
		}
	}
	if retry.After(c.next) {
		c.next = retry
	}
	c.flight = nil
	close(done)
}

func (c *Checker) request(ctx context.Context, etag string) (*Release, string, string, time.Time) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, Endpoint, nil)
	if err != nil {
		return nil, "", "request_failed", time.Time{}
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	request.Header.Set("User-Agent", "MyProbe-release-checker")
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, "", "upstream_unavailable", time.Time{}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		if etag == "" {
			return nil, "", "invalid_response", time.Time{}
		}
		return nil, "", "", time.Time{}
	}
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
		retry := c.now().Add(MinimumInterval)
		// Respect bounded Retry-After values without allowing an upstream value
		// to disable checks indefinitely (or overflow a duration conversion).
		if seconds, err := strconv.ParseInt(response.Header.Get("Retry-After"), 10, 64); err == nil && seconds > 0 && seconds <= 86400 {
			if at := c.now().Add(time.Duration(seconds) * time.Second); at.After(retry) {
				retry = at
			}
		} else if at, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil && at.After(retry) && !at.After(c.now().Add(CheckInterval)) {
			retry = at
		}
		if seconds, err := strconv.ParseInt(response.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			at := time.Unix(seconds, 0)
			if at.After(retry) && at.Before(c.now().Add(24*time.Hour)) {
				retry = at
			}
		}
		return nil, "", "rate_limited", retry
	}
	if response.StatusCode == http.StatusNotFound {
		return nil, "", "no_stable_release", time.Time{}
	}
	if response.StatusCode != http.StatusOK {
		return nil, "", "upstream_unavailable", time.Time{}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		return nil, "", "invalid_response", time.Time{}
	}
	var payload struct {
		Tag         string    `json:"tag_name"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
		PublishedAt time.Time `json:"published_at"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil, "", "invalid_response", time.Time{}
	}
	version, valid := normalize(payload.Tag)
	if !valid || payload.Draft || payload.Prerelease || semver.Prerelease(version) != "" || payload.PublishedAt.IsZero() || payload.PublishedAt.After(c.now()) {
		return nil, "", "invalid_response", time.Time{}
	}
	release := &Release{Tag: payload.Tag, URL: "https://github.com/" + Repository + "/releases/tag/" + url.PathEscape(payload.Tag), PublishedAt: payload.PublishedAt.UTC(), InstallerTag: payload.Tag == semver.Canonical(version)}
	newETag := response.Header.Get("ETag")
	if len(newETag) > 256 || strings.ContainsAny(newETag, "\r\n") {
		newETag = ""
	}
	return release, newETag, "", time.Time{}
}

func normalize(raw string) (string, bool) {
	if raw == "" || len(raw) > 128 || strings.TrimSpace(raw) != raw {
		return "", false
	}
	v := raw
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return "", false
	}
	core := strings.SplitN(strings.SplitN(v, "-", 2)[0], "+", 2)[0]
	return v, strings.Count(core, ".") == 2
}

func Compare(current string, s Snapshot) Comparison {
	r := Comparison{Status: "current_version_unknown", Scope: "software_version_only"}
	version, valid := normalize(current)
	if !valid {
		return r
	}
	r.CurrentVersion = &version
	if s.Status != "available" || s.Latest == nil {
		r.Status = "release_information_unavailable"
		return r
	}
	latest, valid := normalize(s.Latest.Tag)
	if !valid {
		r.Status = "release_information_unavailable"
		return r
	}
	switch semver.Compare(version, latest) {
	case -1:
		r.Status = "update_available"
	case 0:
		r.Status = "up_to_date"
	default:
		r.Status = "ahead_of_stable"
	}
	return r
}
