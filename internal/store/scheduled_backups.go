package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type BackupSchedule struct {
	Enabled           bool     `json:"enabled"`
	Directory         string   `json:"directory"`
	DailyTime         string   `json:"daily_time"`
	Timezone          string   `json:"timezone"`
	Keep              int      `json:"keep"`
	FailureChannelIDs []string `json:"failure_channel_ids"`
}
type BackupRun struct {
	ID            string          `json:"id"`
	ScheduledDate string          `json:"scheduled_date,omitempty"`
	Trigger       string          `json:"trigger"`
	Status        string          `json:"status"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
	ErrorCode     string          `json:"error_code,omitempty"`
	Artifact      json.RawMessage `json:"artifact"`
}

func (s *Store) BackupSchedule(ctx context.Context) (BackupSchedule, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT config_json FROM backup_schedule_state WHERE singleton=1`).Scan(&raw)
	var c BackupSchedule
	if err == nil {
		err = json.Unmarshal([]byte(raw), &c)
	}
	return c, err
}
func (s *Store) SaveBackupSchedule(ctx context.Context, c BackupSchedule) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Acquire the SQLite writer before validating current channel state.
	if _, err = tx.ExecContext(ctx, `UPDATE backup_schedule_state SET singleton=singleton WHERE singleton=1`); err != nil {
		return err
	}
	for _, id := range c.FailureChannelIDs {
		if !c.Enabled {
			break
		}
		var enabled bool
		if err = tx.QueryRowContext(ctx, `SELECT enabled FROM notification_channels WHERE id=?`, id).Scan(&enabled); err != nil || !enabled {
			return errors.New("backup_failure_channel_unavailable")
		}
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE backup_schedule_state SET config_json=? WHERE singleton=1`, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

// BeginBackupRun advances a monotonic local-date high-water mark in the same
// transaction as the claim. Clock rollback, DST repetition and restarts cannot
// generate another daily attempt for an already claimed date.
func (s *Store) BeginBackupRun(ctx context.Context, date string, channels []string, now time.Time) (*BackupRun, error) {
	if now.IsZero() || len(channels) > 20 {
		return nil, errors.New("invalid_backup_claim")
	}
	if date != "" {
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil || parsed.Format("2006-01-02") != date {
			return nil, errors.New("invalid_backup_date")
		}
	}
	seen := map[string]bool{}
	for _, id := range channels {
		if id == "" || len(id) > 128 || seen[id] {
			return nil, errors.New("invalid_backup_failure_channels")
		}
		seen[id] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE backup_schedule_state SET singleton=singleton WHERE singleton=1`); err != nil {
		return nil, err
	}
	var slot any
	trigger := "manual"
	if date != "" {
		var last string
		if err = tx.QueryRowContext(ctx, `SELECT last_scheduled_date FROM backup_schedule_state WHERE singleton=1`).Scan(&last); err != nil {
			return nil, err
		}
		if date <= last {
			return nil, nil
		}
		if _, err = tx.ExecContext(ctx, `UPDATE backup_schedule_state SET last_scheduled_date=? WHERE singleton=1`, date); err != nil {
			return nil, err
		}
		slot = date
		trigger = "daily"
	}
	raw, _ := json.Marshal(channels)
	run := &BackupRun{ID: randomID(), ScheduledDate: date, Trigger: trigger, Status: "running", StartedAt: now.UTC(), Artifact: json.RawMessage("null")}
	if _, err = tx.ExecContext(ctx, `INSERT INTO backup_runs(id,scheduled_date,trigger_kind,status,started_at,failure_channel_ids_json) VALUES(?,?,?,'running',?,?)`, run.ID, slot, trigger, formatTime(now), string(raw)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return run, nil
}

func enqueueBackupFailure(ctx context.Context, tx *sql.Tx, id, code string, now time.Time) error {
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT failure_channel_ids_json FROM backup_runs WHERE id=?`, id).Scan(&raw); err != nil {
		return err
	}
	var channels []string
	if err := json.Unmarshal([]byte(raw), &channels); err != nil {
		return err
	}
	for _, channel := range channels {
		// Disabled/deleted destinations still receive a canceled history record.
		var name, provider string
		var revision int64
		var enabled bool
		err := tx.QueryRowContext(ctx, `SELECT name,COALESCE(NULLIF(provider,''),kind),delivery_revision,enabled FROM notification_channels WHERE id=?`, channel).Scan(&name, &provider, &revision, &enabled)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		status := "pending"
		var finished any
		if err != nil || !enabled {
			status = "canceled"
			finished = formatTime(now)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO system_notifications(id,backup_run_id,channel_id,channel_name,provider,channel_revision,error_code,occurred_at,status,available_at_ms,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(backup_run_id,channel_id) DO NOTHING`, randomID(), id, channel, name, provider, revision, code, formatTime(now), status, now.UnixMilli(), finished)
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) FinishBackupRun(ctx context.Context, id, status, code string, artifact json.RawMessage, now time.Time) error {
	if status != "completed" && status != "failed" && status != "interrupted" {
		return errors.New("invalid_backup_result")
	}
	if now.IsZero() || (status == "completed" && code != "") || (status != "completed" && code == "") {
		return errors.New("invalid_backup_result")
	}
	if code != "" && !deliveryErrorClass.MatchString(code) {
		return errors.New("invalid_backup_error")
	}
	if len(artifact) == 0 {
		artifact = json.RawMessage("null")
	}
	if !json.Valid(artifact) {
		return errors.New("invalid_backup_artifact")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE backup_runs SET status=?,finished_at=?,error_code=?,artifact_json=? WHERE id=? AND status='running'`, status, formatTime(now), code, string(artifact), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("backup_run_not_running")
	}
	if status != "completed" {
		if err = enqueueBackupFailure(ctx, tx, id, code, now); err != nil {
			return err
		}
	}
	// Preserve unfinished notifications; bounded terminal history keeps recent
	// evidence. Daily high-water is stored independently of retained runs.
	if _, err = tx.ExecContext(ctx, `DELETE FROM backup_runs WHERE seq NOT IN (SELECT seq FROM backup_runs ORDER BY seq DESC LIMIT 1000) AND status!='running' AND NOT EXISTS(SELECT 1 FROM system_notifications n WHERE n.backup_run_id=backup_runs.id AND n.status IN ('pending','inflight'))`); err != nil {
		return err
	}
	return tx.Commit()
}

// Caller must own the process-lifetime backup worker lock before reconciliation.
func (s *Store) InterruptBackupRuns(ctx context.Context, now time.Time) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM backup_runs WHERE status='running'`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.FinishBackupRun(ctx, id, "interrupted", "backup_process_interrupted", nil, now); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) BackupRuns(ctx context.Context) ([]BackupRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,COALESCE(scheduled_date,''),trigger_kind,status,started_at,finished_at,error_code,artifact_json FROM backup_runs ORDER BY seq DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []BackupRun{}
	for rows.Next() {
		var r BackupRun
		var start string
		var finish sql.NullString
		var raw string
		if err = rows.Scan(&r.ID, &r.ScheduledDate, &r.Trigger, &r.Status, &start, &finish, &r.ErrorCode, &raw); err != nil {
			return nil, err
		}
		r.StartedAt, err = time.Parse(time.RFC3339Nano, start)
		if err != nil {
			return nil, err
		}
		if finish.Valid {
			at, e := time.Parse(time.RFC3339Nano, finish.String)
			if e != nil {
				return nil, e
			}
			r.FinishedAt = &at
		}
		r.Artifact = json.RawMessage(raw)
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

func (s *Store) BackupRunArtifact(ctx context.Context, id string) (json.RawMessage, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT artifact_json FROM backup_runs WHERE id=? AND status!='running'`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return json.RawMessage(raw), err
}
func (s *Store) RecordBackupRecovery(ctx context.Context, id string, artifact json.RawMessage) error {
	if !json.Valid(artifact) {
		return errors.New("invalid_backup_artifact")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE backup_runs SET artifact_json=? WHERE id=? AND json_extract(artifact_json,'$.id')=json_extract(?,'$.id')`, string(artifact), id, string(artifact))
	return err
}
