package releasecheck

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixtureResponse(status int, body string, header http.Header) *http.Response {
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

const stableBody = `{"tag_name":"v1.2.3","published_at":"2026-10-01T00:00:00Z","draft":false,"prerelease":false,"html_url":"https://untrusted.invalid","body":"private-upstream-marker"}`

func fixedTime() time.Time { return time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC) }

func TestStableCacheCooldownRevalidationAndFailure(t *testing.T) {
	now := fixedTime()
	calls := 0
	c := New(context.Background(), true, transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != Endpoint || r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" || r.Header.Get("User-Agent") != "MyProbe-release-checker" {
			t.Errorf("unexpected request: %v %v", r.URL, r.Header)
		}
		switch calls {
		case 1:
			return fixtureResponse(200, stableBody, http.Header{"Etag": {`"fixture"`}}), nil
		case 2:
			if r.Header.Get("If-None-Match") != `"fixture"` {
				t.Error("missing conditional request")
			}
			return fixtureResponse(304, "", nil), nil
		default:
			return fixtureResponse(500, "private-upstream-marker", nil), nil
		}
	}))
	c.now = func() time.Time { return now }
	s := c.Refresh(context.Background())
	if s.Status != "available" || s.Latest == nil || s.Latest.URL != "https://github.com/"+Repository+"/releases/tag/v1.2.3" || !s.Latest.InstallerTag || !s.LastSuccessAt.Equal(now) {
		t.Fatalf("snapshot: %+v", s)
	}
	s.Latest.Tag = "mutated"
	if c.Snapshot().Latest.Tag != "v1.2.3" {
		t.Fatal("snapshot aliases cache")
	}
	c.Refresh(context.Background())
	if calls != 1 {
		t.Fatal("cooldown bypassed")
	}
	now = now.Add(MinimumInterval)
	s = c.Refresh(context.Background())
	if calls != 2 || s.Status != "available" || !s.LastSuccessAt.Equal(now) {
		t.Fatalf("304: %+v calls=%d", s, calls)
	}
	now = now.Add(MinimumInterval)
	s = c.Refresh(context.Background())
	if s.Status != "stale" || s.ErrorCode != "upstream_unavailable" || Compare("v1.2.3", s).Status != "release_information_unavailable" {
		t.Fatalf("failure promoted stale info: %+v", s)
	}
	now = now.Add(FreshFor + time.Second)
	if c.Snapshot().Status != "stale" {
		t.Fatal("expired cache fresh")
	}
	now = fixedTime().Add(-time.Second)
	if c.Snapshot().Status != "stale" {
		t.Fatal("clock regression fresh")
	}
}

func TestUntrustedReleaseAndStatusBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, code string
	}{
		{"draft", 200, strings.Replace(stableBody, `"draft":false`, `"draft":true`, 1), "invalid_response"},
		{"prerelease flag", 200, strings.Replace(stableBody, `"prerelease":false`, `"prerelease":true`, 1), "invalid_response"},
		{"prerelease tag", 200, strings.Replace(stableBody, "v1.2.3", "v1.2.3-rc.1", 1), "invalid_response"},
		{"shorthand", 200, strings.Replace(stableBody, "v1.2.3", "v1.2", 1), "invalid_response"},
		{"shell tag", 200, strings.Replace(stableBody, "v1.2.3", "v1.2.3;whoami", 1), "invalid_response"},
		{"future", 200, strings.Replace(stableBody, "2026-10-01", "2027-10-01", 1), "invalid_response"},
		{"zero date", 200, `{"tag_name":"v1.2.3"}`, "invalid_response"},
		{"oversized", 200, strings.Repeat("x", maxBody+1), "invalid_response"},
		{"malformed", 200, "{", "invalid_response"},
		{"304 without cache", 304, "", "invalid_response"},
		{"no release", 404, "secret", "no_stable_release"},
		{"redirect", 302, "secret", "upstream_unavailable"},
		{"rate limit", 429, "secret", "rate_limited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(context.Background(), false, transportFunc(func(*http.Request) (*http.Response, error) { return fixtureResponse(tc.status, tc.body, nil), nil }))
			c.now = fixedTime
			s := c.Refresh(context.Background())
			if s.Status != "unavailable" || s.ErrorCode != tc.code || s.Latest != nil || s.LastSuccessAt != nil {
				t.Fatalf("invalid response accepted: %+v", s)
			}
		})
	}
}

func TestConcurrentCheckAndCancelledWaiter(t *testing.T) {
	var calls atomic.Int32
	started, finish := make(chan struct{}), make(chan struct{})
	c := New(context.Background(), false, transportFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		close(started)
		<-finish
		return fixtureResponse(200, stableBody, nil), nil
	}))
	c.now = fixedTime
	ctx, cancel := context.WithCancel(context.Background())
	waiter := make(chan Snapshot, 1)
	go func() { waiter <- c.Refresh(ctx) }()
	<-started
	cancel()
	if s := <-waiter; s.Status != "checking" {
		t.Fatalf("cancelled shared operation: %+v", s)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s := c.Refresh(context.Background()); s.Status != "available" {
				t.Errorf("joined snapshot: %+v", s)
			}
		}()
	}
	close(finish)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent checks=%d", calls.Load())
	}
}

func TestRateLimitAndAutomaticDisable(t *testing.T) {
	for _, header := range []http.Header{
		{"Retry-After": {"3600"}},
		{"Retry-After": {fixedTime().Add(time.Hour).Format(http.TimeFormat)}},
		{"X-Ratelimit-Reset": {fmt.Sprint(fixedTime().Add(time.Hour).Unix())}},
	} {
		c := New(context.Background(), false, transportFunc(func(*http.Request) (*http.Response, error) { return fixtureResponse(429, "", header), nil }))
		c.now = fixedTime
		s := c.Refresh(context.Background())
		if !s.NextCheckAt.Equal(fixedTime().Add(time.Hour)) {
			t.Fatalf("retry ignored: %+v", s)
		}
	}
	var calls atomic.Int32
	c := New(context.Background(), false, transportFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return fixtureResponse(200, stableBody, nil), nil
	}))
	c.now = fixedTime
	c.Run()
	if calls.Load() != 0 || c.Snapshot().Automatic {
		t.Fatal("disabled auto contacted upstream")
	}
	ctx, cancel := context.WithCancel(context.Background())
	c = New(ctx, true, transportFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		cancel()
		return fixtureResponse(200, stableBody, nil), nil
	}))
	c.now = fixedTime
	c.Run()
	if calls.Load() != 1 {
		t.Fatal("startup check absent")
	}
}

func TestVersionComparisonDoesNotInventCompatibility(t *testing.T) {
	s := Snapshot{Status: "available", Latest: &Release{Tag: "v1.2.3"}}
	for _, tc := range []struct{ version, want string }{
		{"v1.2.2", "update_available"}, {"1.2.3", "up_to_date"}, {"v1.2.3+build.4", "up_to_date"},
		{"v1.2.3-rc.1", "update_available"}, {"v1.3.0", "ahead_of_stable"}, {"v1.10.0", "ahead_of_stable"},
		{"dev", "current_version_unknown"}, {"", "current_version_unknown"}, {"v1.2", "current_version_unknown"}, {" v1.2.3", "current_version_unknown"},
	} {
		if got := Compare(tc.version, s); got.Status != tc.want || got.Scope != "software_version_only" {
			t.Errorf("%q: %+v", tc.version, got)
		}
	}
	for _, status := range []string{"not_checked", "checking", "stale", "unavailable"} {
		s.Status = status
		if Compare("v1.2.3", s).Status != "release_information_unavailable" {
			t.Errorf("%s considered latest", status)
		}
	}
}

func TestFreshnessWithoutFailureAndLifetimeBound(t *testing.T) {
	now := fixedTime()
	c := New(context.Background(), false, transportFunc(func(*http.Request) (*http.Response, error) { return fixtureResponse(200, stableBody, nil), nil }))
	c.now = func() time.Time { return now }
	c.Refresh(context.Background())
	now = now.Add(FreshFor)
	if c.Snapshot().Status != "available" {
		t.Fatal("freshness boundary excluded")
	}
	now = now.Add(time.Nanosecond)
	if c.Snapshot().Status != "stale" || Compare("v1.2.3", c.Snapshot()).Status != "release_information_unavailable" {
		t.Fatal("expired cache promoted to up-to-date")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	c = New(ctx, false, transportFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			t.Error("unbounded upstream context")
		}
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	c.now = fixedTime
	done := make(chan Snapshot, 1)
	go func() { done <- c.Refresh(context.Background()) }()
	<-started
	cancel()
	select {
	case s := <-done:
		if s.Status != "unavailable" {
			t.Fatal(s)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel check")
	}
}

func TestRedirectNotFollowedAndUnconditional304NotRevalidated(t *testing.T) {
	calls := 0
	c := New(context.Background(), false, transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return fixtureResponse(302, "", http.Header{"Location": {"https://untrusted.invalid/redirect"}}), nil
	}))
	c.now = fixedTime
	if s := c.Refresh(context.Background()); s.ErrorCode != "upstream_unavailable" || calls != 1 {
		t.Fatalf("redirect followed: snapshot=%+v calls=%d", s, calls)
	}
	now := fixedTime()
	calls = 0
	c = New(context.Background(), false, transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return fixtureResponse(200, stableBody, http.Header{"Etag": {strings.Repeat("x", 257)}}), nil
		}
		return fixtureResponse(304, "", nil), nil
	}))
	c.now = func() time.Time { return now }
	c.Refresh(context.Background())
	now = now.Add(MinimumInterval)
	if s := c.Refresh(context.Background()); s.Status != "stale" || s.ErrorCode != "invalid_response" || !s.LastSuccessAt.Equal(fixedTime()) {
		t.Fatalf("unconditional 304 revalidated: %+v", s)
	}
}
