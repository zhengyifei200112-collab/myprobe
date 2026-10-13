package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/notificationlink"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

const diagnosticTimeKey = `substr(substr(scheduled_at,1,19)||'.'||rtrim(substr(scheduled_at,21),'Z')||'000000000',1,29)`

type HTTPDiagnosticObservation struct {
	TaskID      string     `json:"task_id"`
	Revision    int64      `json:"revision"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	Status      string     `json:"status"`
	ErrorClass  string     `json:"error_class,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	StatusCode  int        `json:"status_code,omitempty"`
	DurationMS  *float64   `json:"duration_ms,omitempty"`
}

type HTTPDiagnosticObserver struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	CurrentlyAssigned bool   `json:"currently_assigned"`
}

type HTTPDiagnostics struct {
	Service      HTTPServiceSummary          `json:"service"`
	Observer     HTTPDiagnosticObserver      `json:"observer"`
	Statistics   HTTPStatistics              `json:"statistics"`
	Observations []HTTPDiagnosticObservation `json:"observations"`
	NextCursor   string                      `json:"next_cursor"`
}

type diagnosticCursor struct {
	At    string `json:"at"`
	ID    string `json:"id"`
	Scope string `json:"scope"`
}

func diagnosticKey(at time.Time) string { return at.UTC().Format("2006-01-02T15:04:05.000000000") }

// HTTPServiceDiagnostics reads only safe columns, never private task specs. All
// components of a page share one read snapshot; later pages are fresh snapshots.
func (s *Store) HTTPServiceDiagnostics(ctx context.Context, serviceID, nodeID string, start, end, now time.Time, limit int, after string) (HTTPDiagnostics, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return HTTPDiagnostics{}, err
	}
	defer tx.Rollback()
	return readHTTPServiceDiagnostics(ctx, tx, serviceID, nodeID, start, end, now, limit, after)
}

func readHTTPServiceDiagnostics(ctx context.Context, tx *sql.Tx, serviceID, nodeID string, start, end, now time.Time, limit int, after string) (HTTPDiagnostics, error) {
	result := HTTPDiagnostics{Observations: []HTTPDiagnosticObservation{}}
	if !notificationlink.ValidDiagnosticContext(serviceID, nodeID, start, end) || now.Year() < 1970 || now.Year() > 2100 || limit < 1 || limit > 100 || len(after) > 512 {
		return result, ErrInvalidHTTPService
	}
	scopeBytes := sha256.Sum256([]byte(serviceID + "\x00" + nodeID + "\x00" + start.UTC().Format(time.RFC3339Nano) + "\x00" + end.UTC().Format(time.RFC3339Nano)))
	scope := hex.EncodeToString(scopeBytes[:])
	var cursor diagnosticCursor
	if after != "" {
		body, err := base64.RawURLEncoding.DecodeString(after)
		if err != nil || json.Unmarshal(body, &cursor) != nil || cursor.Scope != scope {
			return result, ErrInvalidHTTPService
		}
		at, err := time.Parse("2006-01-02T15:04:05.000000000", cursor.At)
		if err != nil || cursor.At != diagnosticKey(at) || at.Before(start) || !at.Before(end) || !notificationlink.ValidDiagnosticContext(cursor.ID, nodeID, start, end) {
			return result, ErrInvalidHTTPService
		}
	}
	var err error
	var updated string
	err = tx.QueryRowContext(ctx, `SELECT id,name,revision,enabled,interval_seconds,updated_at,(SELECT COUNT(*) FROM http_service_nodes WHERE service_id=http_services.id) FROM http_services WHERE id=?`, serviceID).Scan(&result.Service.ID, &result.Service.Name, &result.Service.Revision, &result.Service.Enabled, &result.Service.IntervalSeconds, &updated, &result.Service.NodeCount)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if result.Service.UpdatedAt, err = parseTime(updated); err != nil {
		return result, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id,name,EXISTS(SELECT 1 FROM http_service_nodes WHERE service_id=? AND node_id=nodes.id) FROM nodes WHERE id=?`, serviceID, nodeID).Scan(&result.Observer.ID, &result.Observer.Name, &result.Observer.CurrentlyAssigned)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if result.Statistics, err = readHTTPServiceStatistics(ctx, tx, serviceID, nodeID, start, end, now); err != nil {
		return result, err
	}
	from := start
	if from.Before(result.Statistics.RetainedFrom) {
		from = result.Statistics.RetainedFrom
	}
	query := `SELECT id,revision,scheduled_at,expires_at,result_json FROM http_tasks WHERE service_id=? AND node_id=? AND ` + diagnosticTimeKey + `>=? AND ` + diagnosticTimeKey + `<?`
	args := []any{serviceID, nodeID, diagnosticKey(from), diagnosticKey(end)}
	if after != "" {
		query += ` AND ` + diagnosticTimeKey + `<=? AND (` + diagnosticTimeKey + `<? OR id<?)`
		args = append(args, cursor.At, cursor.At, cursor.ID)
	}
	query += ` ORDER BY ` + diagnosticTimeKey + ` DESC,id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item HTTPDiagnosticObservation
		var scheduled, expires string
		var raw sql.NullString
		if err = rows.Scan(&item.TaskID, &item.Revision, &scheduled, &expires, &raw); err != nil {
			return result, err
		}
		if item.ScheduledAt, err = parseTime(scheduled); err != nil {
			return result, err
		}
		if item.ExpiresAt, err = parseTime(expires); err != nil {
			return result, err
		}
		if !notificationlink.ValidDiagnosticContext(item.TaskID, nodeID, start, end) || item.Revision < 1 || !item.ExpiresAt.After(item.ScheduledAt) {
			return result, errors.New("invalid stored diagnostic task")
		}
		if len(result.Observations) == limit {
			last := result.Observations[limit-1]
			body, _ := json.Marshal(diagnosticCursor{At: diagnosticKey(last.ScheduledAt), ID: last.TaskID, Scope: scope})
			result.NextCursor = base64.RawURLEncoding.EncodeToString(body)
			break
		}
		item.Status = "pending"
		if now.After(item.ExpiresAt.Add(time.Minute)) {
			item.Status = "missing"
		}
		if raw.Valid {
			var evidence httpcheck.Result
			if json.Unmarshal([]byte(raw.String), &evidence) != nil || !validDiagnosticEvidence(evidence, item, serviceID, now) {
				return result, errors.New("invalid stored diagnostic evidence")
			}
			item.Status, item.ErrorClass, item.CompletedAt = evidence.Outcome, evidence.ErrorClass, &evidence.CompletedAt
			if evidence.Outcome != "unobserved" {
				item.StatusCode, item.DurationMS = evidence.StatusCode, &evidence.DurationMS
			}
		}
		result.Observations = append(result.Observations, item)
	}
	return result, rows.Err()
}

func validDiagnosticEvidence(r httpcheck.Result, item HTTPDiagnosticObservation, serviceID string, now time.Time) bool {
	if r.TaskID != item.TaskID || r.ServiceID != serviceID || r.Revision != uint64(item.Revision) || item.Revision < 1 || !r.ScheduledAt.Equal(item.ScheduledAt) || r.CompletedAt.Before(item.ScheduledAt) || r.CompletedAt.After(item.ExpiresAt.Add(time.Second)) || r.CompletedAt.After(now.Add(time.Minute)) || math.IsNaN(r.DurationMS) || math.IsInf(r.DurationMS, 0) || r.DurationMS < 0 || r.DurationMS > 61000 || (r.StatusCode != 0 && (r.StatusCode < 100 || r.StatusCode > 599)) {
		return false
	}
	switch r.Outcome {
	case "success":
		return r.ErrorClass == "" && r.StatusCode != 0
	case "failure":
		switch r.ErrorClass {
		case "dns", "refused", "timeout", "tls_invalid", "certificate_expired", "response_too_large", "redirect_limit":
			return true
		case "status_mismatch", "content_mismatch":
			return r.StatusCode != 0
		}
	case "unobserved":
		if r.StatusCode != 0 {
			return false
		}
		switch r.ErrorClass {
		case "unsupported", "busy", "cancelled", "invalid_task", "policy_denied", "internal":
			return true
		}
	}
	return false
}
