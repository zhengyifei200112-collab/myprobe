package store

import "time"

// Original report timestamps stay RFC3339Nano; the SQLite virtual raw-sample key
// and query arguments share a fixed fractional width for exact ordering.
func formatCapturedTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
}
