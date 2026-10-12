package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type IncidentSilenceRecord struct {
	IncidentSilence
	Active     bool                  `json:"active"`
	Decisions  []MaintenanceDecision `json:"decisions"`
	RecordedAt *time.Time            `json:"recorded_at,omitempty"`
}
type IncidentSilencePage struct {
	IncidentState string                  `json:"incident_state,omitempty"`
	CheckedAt     time.Time               `json:"checked_at"`
	ActiveSilence *IncidentSilence        `json:"active_silence"`
	Silences      []IncidentSilenceRecord `json:"silences"`
	NextCursor    string                  `json:"next_cursor"`
}

// Each page is one snapshot. Delivery pages contain only recorded causal links,
// never inferred matches based on overlapping dates.
func (s *Store) listSilences(ctx context.Context, id, after string, limit int, at time.Time, delivery bool) (IncidentSilencePage, error) {
	page := IncidentSilencePage{CheckedAt: at.UTC().Truncate(time.Millisecond), Silences: []IncidentSilenceRecord{}}
	if !portableID.MatchString(id) || (after != "" && !portableID.MatchString(after)) || limit < 1 || limit > 100 || !validSilenceTime(at) {
		return page, ErrInvalidSilence
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	incidentID := id
	if delivery {
		err = tx.QueryRowContext(ctx, `SELECT incident_id FROM notification_deliveries WHERE id=?`, id).Scan(&incidentID)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT state FROM alert_incidents WHERE id=?`, id).Scan(&page.IncidentState)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return page, ErrNotFound
	}
	if err != nil {
		return page, err
	}
	if !delivery {
		active, err := matchingSilenceIDs(ctx, tx, incidentID, time.Time{}, page.CheckedAt, false)
		if err != nil {
			return page, err
		}
		if len(active) > 0 {
			w, err := scanSilence(tx.QueryRowContext(ctx, `SELECT `+silenceColumns+` FROM incident_silences WHERE id=?`, active[0]))
			if err != nil {
				return page, err
			}
			page.ActiveSilence = &w
		}
	}
	query := `SELECT ` + silenceColumns + ` FROM incident_silences WHERE incident_id=? AND id>?`
	args := []any{incidentID, after}
	if delivery {
		query += ` AND id IN (SELECT silence_id FROM delivery_silence_links WHERE delivery_id=?)`
		args = append(args, id)
	}
	query += ` ORDER BY id LIMIT ?`
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		w, err := scanSilence(rows)
		if err != nil {
			rows.Close()
			return page, err
		}
		page.Silences = append(page.Silences, IncidentSilenceRecord{IncidentSilence: w, Decisions: []MaintenanceDecision{}, Active: !page.CheckedAt.Before(w.StartsAt) && page.CheckedAt.Before(w.EndsAt) && (w.CanceledAt == nil || page.CheckedAt.Before(*w.CanceledAt)) && (w.ClosedAt == nil || page.CheckedAt.Before(*w.ClosedAt))})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if len(page.Silences) > limit {
		page.Silences = page.Silences[:limit]
		page.NextCursor = page.Silences[limit-1].ID
	}
	for n := range page.Silences {
		item := &page.Silences[n]
		if delivery {
			var recorded int64
			if err := tx.QueryRowContext(ctx, `SELECT recorded_at_ms FROM delivery_silence_links WHERE delivery_id=? AND silence_id=?`, id, item.ID).Scan(&recorded); err != nil {
				return page, err
			}
			stamp := time.UnixMilli(recorded).UTC()
			item.RecordedAt = &stamp
		}
		rows, err := tx.QueryContext(ctx, `SELECT kind,first_at_ms,last_at_ms FROM incident_silence_decisions WHERE silence_id=? ORDER BY kind`, item.ID)
		if err != nil {
			return page, err
		}
		for rows.Next() {
			var d MaintenanceDecision
			var first, last int64
			if err := rows.Scan(&d.Kind, &first, &last); err != nil {
				rows.Close()
				return page, err
			}
			d.FirstAt, d.LastAt = time.UnixMilli(first).UTC(), time.UnixMilli(last).UTC()
			item.Decisions = append(item.Decisions, d)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return page, err
		}
	}
	return page, tx.Commit()
}
func (s *Store) ListIncidentSilences(ctx context.Context, id, after string, limit int, at time.Time) (IncidentSilencePage, error) {
	return s.listSilences(ctx, id, after, limit, at, false)
}
func (s *Store) ListDeliverySilences(ctx context.Context, id, after string, limit int, at time.Time) (IncidentSilencePage, error) {
	return s.listSilences(ctx, id, after, limit, at, true)
}
