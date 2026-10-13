package store

import (
	"context"
	"database/sql"
	"time"
)

type CertificateReminder struct {
	Seq               int64     `json:"seq"`
	CertificateSHA256 string    `json:"certificate_sha256"`
	DaysBefore        int       `json:"days_before"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
	QueuedOnce        bool      `json:"queued_once"`
	AuthorizedOnce    bool      `json:"authorized_once"`
	Skipped           bool      `json:"skipped"`
	IncidentID        string    `json:"incident_id,omitempty"`
	DeliveryStatus    string    `json:"delivery_status"`
	ObservationStale  bool      `json:"observation_stale"`
	ResolutionReason  string    `json:"resolution_reason,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

func (s *Store) ListCertificateReminders(ctx context.Context, serviceID string, before int64, limit int) ([]CertificateReminder, error) {
	if serviceID == "" || len(serviceID) > 128 || before < 0 || limit < 1 || limit > 100 {
		return nil, ErrInvalidHTTPService
	}
	rows, err := s.db.QueryContext(ctx, `SELECT l.seq,l.certificate_sha256,l.days_before,l.not_before,l.not_after,l.queued_once,l.authorized_once,l.skipped,COALESCE(l.incident_id,''),d.status,COALESCE(i.observation_stale,0),COALESCE(i.resolution_reason,''),l.created_at FROM http_certificate_reminders l LEFT JOIN notification_deliveries d ON d.id=l.delivery_id LEFT JOIN alert_incidents i ON i.id=l.incident_id WHERE l.service_id=? AND (?=0 OR l.seq<?) ORDER BY l.seq DESC LIMIT ?`, serviceID, before, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CertificateReminder{}
	for rows.Next() {
		var v CertificateReminder
		var first, expiry, created string
		var status sql.NullString
		if err := rows.Scan(&v.Seq, &v.CertificateSHA256, &v.DaysBefore, &first, &expiry, &v.QueuedOnce, &v.AuthorizedOnce, &v.Skipped, &v.IncidentID, &status, &v.ObservationStale, &v.ResolutionReason, &created); err != nil {
			return nil, err
		}
		if v.NotBefore, err = parseTime(first); err != nil {
			return nil, err
		}
		if v.NotAfter, err = parseTime(expiry); err != nil {
			return nil, err
		}
		if v.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		v.DeliveryStatus = "not_queued"
		if status.Valid {
			v.DeliveryStatus = status.String
		} else if v.QueuedOnce {
			v.DeliveryStatus = "history_unavailable"
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
