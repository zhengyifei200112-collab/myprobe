package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

type ReportIngress struct {
	Status      string     `json:"status"`
	Transport   string     `json:"transport,omitempty"`
	WireVersion *int       `json:"wire_version,omitempty"`
	ObservedAt  *time.Time `json:"observed_at,omitempty"`
	CapturedAt  *time.Time `json:"captured_at,omitempty"`
}
type ingressSource struct {
	transport string
	version   int
}

// SaveReportWithIngress records the actual authenticated handler that accepted
// a report. Connection presence or hello metadata cannot supply this evidence.
func (s *Store) SaveReportWithIngress(ctx context.Context, nodeID string, report protocol.Report, transport string, version int) error {
	if (transport != "http" && transport != "websocket") || (version != 1 && version != 2) {
		return errors.New("invalid_report_ingress")
	}
	return s.saveReport(ctx, nodeID, report, &ingressSource{transport, version})
}
func recordReportIngress(ctx context.Context, tx *sql.Tx, nodeID string, captured, observed time.Time, source *ingressSource) error {
	transport := "unknown"
	var version any
	if source != nil {
		transport = source.transport
		version = source.version
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO node_report_ingress(node_id,transport,wire_version,observed_at,captured_at) VALUES(?,?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET transport=excluded.transport,wire_version=excluded.wire_version,observed_at=excluded.observed_at,captured_at=excluded.captured_at`, nodeID, transport, version, formatTime(observed), formatTime(captured))
	return err
}
func (s *Store) LastReportIngress(ctx context.Context, nodeID string) (ReportIngress, error) {
	result := ReportIngress{Status: "unavailable"}
	var transport, observed, captured string
	var version sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT transport,wire_version,observed_at,captured_at FROM node_report_ingress WHERE node_id=?`, nodeID).Scan(&transport, &version, &observed, &captured)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	at, err := parseTime(observed)
	if err != nil {
		return result, err
	}
	sample, err := parseTime(captured)
	if err != nil {
		return result, err
	}
	result.ObservedAt = &at
	result.CapturedAt = &sample
	if transport == "unknown" {
		return result, nil
	}
	if !version.Valid || (transport != "http" && transport != "websocket") || (version.Int64 != 1 && version.Int64 != 2) {
		return result, errors.New("invalid_stored_report_ingress")
	}
	wire := int(version.Int64)
	result.Status = "observed"
	result.Transport = transport
	result.WireVersion = &wire
	return result, nil
}
