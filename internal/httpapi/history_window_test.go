package httpapi

import (
	"net/url"
	"testing"
	"time"
)

func TestHistoryWindowPresetsUseOneClock(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 123, time.UTC)
	for _, name := range []string{"", "1h", "12h", "1d", "3d", "7d", "30d", "1y"} {
		window, err := parseHistoryWindow(url.Values{"range": {name}}, now)
		if err != nil {
			t.Fatal(err)
		}
		want := name
		if want == "" {
			want = "1h"
		}
		duration, bucket, _ := historyRange(want)
		if window.Name != want || !window.End.Equal(now) || !window.Start.Equal(now.Add(-duration)) || window.BucketSeconds != bucket {
			t.Fatalf("preset %q: %+v", name, window)
		}
	}
}

func TestHistoryWindowAbsoluteBoundsAndResolution(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	zone := time.FixedZone("local", 8*3600)
	for _, duration := range []time.Duration{time.Nanosecond, time.Hour, time.Hour + 1, 12 * time.Hour, 24 * time.Hour, 3 * 24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour, 365 * 24 * time.Hour} {
		start := now.Add(-duration)
		window, err := parseHistoryWindow(url.Values{
			"start": {start.In(zone).Format(time.RFC3339Nano)},
			"end":   {now.Format(time.RFC3339Nano)},
		}, now)
		if err != nil || window.Name != "custom" || !window.Start.Equal(start) || !window.End.Equal(now) || window.Start.Location() != time.UTC {
			t.Fatalf("duration %v: %+v, %v", duration, window, err)
		}
		if points := int64(duration/time.Second)/int64(window.BucketSeconds) + 2; points > 2000 {
			t.Fatalf("duration %v yields %d points", duration, points)
		}
	}
}

func TestHistoryWindowRejectsAmbiguousOrUnboundedQueries(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour).Format(time.RFC3339Nano)
	end := now.Format(time.RFC3339Nano)
	for name, query := range map[string]url.Values{
		"unknown preset":      {"range": {"forever"}},
		"duplicate preset":    {"range": {"1h", "1y"}},
		"missing end":         {"start": {start}},
		"missing start":       {"end": {end}},
		"empty start":         {"start": {""}, "end": {end}},
		"invalid timestamp":   {"start": {"yesterday"}, "end": {end}},
		"missing timezone":    {"start": {"2026-10-03T11:00:00"}, "end": {end}},
		"mixed selectors":     {"range": {"1h"}, "start": {start}, "end": {end}},
		"duplicate bound":     {"start": {start, start}, "end": {end}},
		"equal bounds":        {"start": {end}, "end": {end}},
		"reversed":            {"start": {end}, "end": {start}},
		"future":              {"start": {start}, "end": {now.Add(time.Nanosecond).Format(time.RFC3339Nano)}},
		"over one year":       {"start": {now.Add(-365*24*time.Hour - time.Nanosecond).Format(time.RFC3339Nano)}, "end": {end}},
		"before epoch":        {"start": {"1969-12-31T23:00:00Z"}, "end": {"1970-01-01T00:00:00Z"}},
		"offset before epoch": {"start": {"1970-01-01T00:00:00+08:00"}, "end": {"1970-01-01T00:00:00Z"}},
	} {
		t.Run(name, func(t *testing.T) {
			if window, err := parseHistoryWindow(query, now); err == nil {
				t.Fatalf("accepted invalid query: %+v", window)
			}
		})
	}
}
