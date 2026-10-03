package httpapi

import (
	"errors"
	"net/url"
	"time"
)

// historyWindow describes a half-open interval. Parsing is separate from reads so
// malformed or excessive ranges can be rejected before any history query runs.
type historyWindow struct {
	Name          string
	Start         time.Time
	End           time.Time
	BucketSeconds int
}

func parseHistoryWindow(query url.Values, now time.Time) (historyWindow, error) {
	invalid := errors.New("use one preset range or both RFC3339 start and end; maximum duration is 365 days and end cannot be in the future")
	for _, key := range []string{"range", "start", "end"} {
		if len(query[key]) > 1 {
			return historyWindow{}, invalid
		}
	}
	_, hasStart := query["start"]
	_, hasEnd := query["end"]
	if !hasStart && !hasEnd {
		name := query.Get("range")
		if name == "" {
			name = "1h"
		}
		duration, bucket, ok := historyRange(name)
		if !ok {
			return historyWindow{}, invalid
		}
		return historyWindow{Name: name, Start: now.UTC().Add(-duration), End: now.UTC(), BucketSeconds: bucket}, nil
	}
	if !hasStart || !hasEnd || query.Has("range") {
		return historyWindow{}, invalid
	}
	start, err := time.Parse(time.RFC3339Nano, query.Get("start"))
	if err != nil {
		return historyWindow{}, invalid
	}
	end, err := time.Parse(time.RFC3339Nano, query.Get("end"))
	if err != nil || !start.Before(end) || end.After(now) || start.Year() < 1970 || end.Sub(start) > 365*24*time.Hour {
		return historyWindow{}, invalid
	}
	// Reuse the established preset resolutions. Every selected interval has at
	// most 2000 aligned buckets per series, including partial boundary buckets.
	for _, name := range []string{"1h", "12h", "1d", "3d", "7d", "30d", "1y"} {
		duration, bucket, _ := historyRange(name)
		if end.Sub(start) <= duration {
			return historyWindow{Name: "custom", Start: start.UTC(), End: end.UTC(), BucketSeconds: bucket}, nil
		}
	}
	return historyWindow{}, invalid
}
