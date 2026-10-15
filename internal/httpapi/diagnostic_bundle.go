package httpapi

import (
	"context"
	"math"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/diagnostics"
	"github.com/zhengyifei200112-collab/myprobe/internal/scheduler"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type diagnosticJob struct {
	State           string     `json:"state"`
	Active          uint64     `json:"active"`
	Completed       uint64     `json:"completed"`
	Failed          uint64     `json:"failed"`
	Cancelled       uint64     `json:"cancelled"`
	ErrorClass      string     `json:"error_class,omitempty"`
	LastCompletedAt *time.Time `json:"last_completed_at,omitempty"`
	LastSuccessAt   *time.Time `json:"last_success_at,omitempty"`
	DurationSeconds *float64   `json:"duration_seconds,omitempty"`
}
type diagnosticWorker struct {
	Status     string `json:"status"`
	ErrorClass string `json:"error_class,omitempty"`
	Operation  string `json:"operation,omitempty"`
}
type diagnosticRuntime struct {
	Version       diagnostics.VersionObservation `json:"version"`
	GoVersion     diagnostics.VersionObservation `json:"go_version"`
	OS            string                         `json:"os"`
	Architecture  string                         `json:"architecture"`
	UptimeSeconds *float64                       `json:"uptime_seconds,omitempty"`
}
type diagnosticShape struct {
	HTTPExecutionEnabled              bool     `json:"http_execution_enabled"`
	HTTPSOnlyCookie                   bool     `json:"https_only_cookie"`
	TrustedProxyEntries               int      `json:"trusted_proxy_entries"`
	ApplicationKeyConfigured          bool     `json:"application_key_configured"`
	NotificationOriginConfigured      bool     `json:"notification_origin_configured"`
	IndependentBackupSecretConfigured bool     `json:"independent_backup_secret_configured"`
	RawRetentionSeconds               *float64 `json:"raw_retention_seconds,omitempty"`
	MinuteRetentionSeconds            *float64 `json:"minute_retention_seconds,omitempty"`
	FiveMinuteRetentionSeconds        *float64 `json:"five_minute_retention_seconds,omitempty"`
	RetentionIntervalSeconds          *float64 `json:"retention_interval_seconds,omitempty"`
}
type diagnosticDatabase struct {
	Status             string `json:"status"`
	SchemaVersion      string `json:"schema_version,omitempty"`
	JournalMode        string `json:"journal_mode,omitempty"`
	AllocatedBytes     *int64 `json:"allocated_bytes,omitempty"`
	ReusableBytes      *int64 `json:"reusable_bytes,omitempty"`
	DatabaseBytes      *int64 `json:"database_bytes,omitempty"`
	WALBytes           *int64 `json:"wal_bytes,omitempty"`
	DatabaseFileStatus string `json:"database_file_status"`
	WALFileStatus      string `json:"wal_file_status"`
}
type diagnosticQueue struct {
	Status           string                 `json:"status"`
	CountingContract string                 `json:"counting_contract"`
	Incident         *diagnosticQueueCounts `json:"incident,omitempty"`
	Aggregate        *diagnosticQueueCounts `json:"aggregate,omitempty"`
	System           *diagnosticQueueCounts `json:"system,omitempty"`
}
type diagnosticQueueCounts struct {
	Pending       int64 `json:"pending"`
	DueByTime     int64 `json:"due_by_time"`
	Future        int64 `json:"future"`
	Inflight      int64 `json:"inflight"`
	ExpiredLeases int64 `json:"expired_leases"`
}
type diagnosticTransport struct {
	Status               string `json:"status"`
	AgentConnections     *int   `json:"agent_connections,omitempty"`
	V1Connections        *int   `json:"v1_connections,omitempty"`
	V2Connections        *int   `json:"v2_connections,omitempty"`
	UnknownConnections   *int   `json:"unknown_connections,omitempty"`
	PendingResults       *int   `json:"pending_results,omitempty"`
	ExpiredResults       *int   `json:"expired_results,omitempty"`
	HTTPStatus           string `json:"http_status"`
	HTTPPending          *int   `json:"http_pending,omitempty"`
	HTTPExpired          *int   `json:"http_expired,omitempty"`
	BrowserSubscriptions *int   `json:"browser_subscriptions,omitempty"`
}
type diagnosticBundle struct {
	Format           string                        `json:"format"`
	GeneratedAt      time.Time                     `json:"generated_at"`
	ObservationScope string                        `json:"observation_scope"`
	Included         []string                      `json:"included"`
	Excluded         []string                      `json:"excluded"`
	Runtime          diagnosticRuntime             `json:"runtime"`
	Shape            diagnosticShape               `json:"configuration_structure"`
	Configuration    store.ConfigurationDiagnostic `json:"configuration_counts"`
	Database         diagnosticDatabase            `json:"database"`
	Jobs             map[string]diagnosticJob      `json:"jobs"`
	Workers          map[string]diagnosticWorker   `json:"workers"`
	Queue            diagnosticQueue               `json:"notification_queue"`
	Transport        diagnosticTransport           `json:"transport"`
}

func exportEnum(value string, allowed ...string) string {
	for _, known := range allowed {
		if value == known {
			return known
		}
	}
	return "unavailable"
}
func exportTime(at *time.Time) *time.Time {
	if at == nil || at.IsZero() || at.Year() < 1 || at.Year() > 9999 {
		return nil
	}
	copy := at.UTC()
	return &copy
}
func exportFloat(value *float64) *float64 {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
		return nil
	}
	copy := *value
	return &copy
}
func exportInt64(value *int64) *int64 {
	if value == nil || *value < 0 {
		return nil
	}
	copy := *value
	return &copy
}
func exportInt(value *int) *int {
	if value == nil || *value < 0 {
		return nil
	}
	copy := *value
	return &copy
}
func exportJob(job diagnostics.JobSnapshot) diagnosticJob {
	result := diagnosticJob{State: exportEnum(job.State, "never_run", "running", "success", "failed", "cancelled"), Active: job.ActiveRuns, Completed: job.CompletedRuns, Failed: job.FailedRuns, Cancelled: job.CancelledRuns, LastCompletedAt: exportTime(job.LastCompletedAt), LastSuccessAt: exportTime(job.LastSuccessAt), DurationSeconds: exportFloat(job.LastDurationSeconds)}
	if job.LastErrorCode != "" {
		result.ErrorClass = exportEnum(job.LastErrorCode, "operation_failed", "deadline_exceeded", "cancelled")
	}
	return result
}
func exportQueueCounts(counts *store.QueueCounts) *diagnosticQueueCounts {
	if counts == nil {
		return nil
	}
	return &diagnosticQueueCounts{Pending: counts.Pending, DueByTime: counts.PendingDueByTime, Future: counts.PendingFuture, Inflight: counts.Inflight, ExpiredLeases: counts.ExpiredLeases}
}

var diagnosticSchemaVersion = regexp.MustCompile(`^[0-9]{3}$`)

// Explicit projections are the export boundary. Never serialize systemHealth,
// SiteSettings, node DTOs or arbitrary observer maps as a diagnostic document.
func (s *Server) collectDiagnosticBundle(ctx context.Context) diagnosticBundle {
	now := time.Now()
	bundle := diagnosticBundle{Format: "myprobe.diagnostics.v1", GeneratedAt: now.UTC(), ObservationScope: "separate_process_and_database_observations",
		Included: []string{"normalized_versions", "configuration_structure", "migration_and_database_sizes", "configuration_counts", "active_queue_counts", "job_and_worker_state", "transport_counts"},
		Excluded: []string{"credentials_and_tokens", "database_contents", "backup_keys_and_paths", "addresses_and_target_urls", "names_notes_and_hostnames", "payloads_sessions_and_audit_contents", "arbitrary_version_labels"},
		Runtime:  diagnosticRuntime{Version: diagnostics.ObserveVersion(s.buildVersion), GoVersion: diagnostics.ObserveVersion(strings.TrimPrefix(runtime.Version(), "go")), OS: runtime.GOOS, Architecture: runtime.GOARCH},
		Jobs:     map[string]diagnosticJob{}, Workers: map[string]diagnosticWorker{}, Transport: diagnosticTransport{Status: "unavailable", HTTPStatus: "unavailable"}}
	if !s.processStartedAt.IsZero() && !s.processStartedAt.After(now) {
		seconds := now.Sub(s.processStartedAt).Seconds()
		bundle.Runtime.UptimeSeconds = &seconds
	}
	cfg := s.config
	bundle.Shape = diagnosticShape{HTTPExecutionEnabled: cfg.HTTPProbesEnabled, HTTPSOnlyCookie: cfg.CookieSecure, TrustedProxyEntries: len(cfg.TrustedProxies), ApplicationKeyConfigured: len(cfg.EncryptionKey) >= 32, NotificationOriginConfigured: cfg.NotificationOrigin != "", IndependentBackupSecretConfigured: cfg.BackupPassphraseFile != ""}
	for _, item := range []struct {
		value time.Duration
		out   **float64
	}{{cfg.Retention.Raw, &bundle.Shape.RawRetentionSeconds}, {cfg.Retention.OneMinute, &bundle.Shape.MinuteRetentionSeconds}, {cfg.Retention.FiveMinute, &bundle.Shape.FiveMinuteRetentionSeconds}, {cfg.Retention.Interval, &bundle.Shape.RetentionIntervalSeconds}} {
		if item.value > 0 {
			seconds := item.value.Seconds()
			*item.out = &seconds
		}
	}
	bundle.Configuration = s.store.ConfigurationDiagnostic(ctx)
	db := s.store.DatabaseDiagnostics(ctx)
	bundle.Database = diagnosticDatabase{Status: exportEnum(db.Status, "ok", "unavailable"), JournalMode: exportEnum(db.JournalMode, "wal", "delete", "truncate", "persist", "memory", "off"), AllocatedBytes: exportInt64(db.AllocatedBytes), ReusableBytes: exportInt64(db.ReusableBytes), DatabaseBytes: exportInt64(db.DatabaseFile.Bytes), WALBytes: exportInt64(db.WALFile.Bytes), DatabaseFileStatus: exportEnum(db.DatabaseFile.Status, "ok", "absent", "not_applicable", "unavailable"), WALFileStatus: exportEnum(db.WALFile.Status, "ok", "absent", "not_applicable", "unavailable")}
	if diagnosticSchemaVersion.MatchString(db.SchemaVersion) {
		bundle.Database.SchemaVersion = db.SchemaVersion
	}
	queue := s.store.NotificationQueueDiagnostics(ctx, now)
	bundle.Queue = diagnosticQueue{Status: exportEnum(queue.Status, "available", "unavailable"), CountingContract: "incident_logical_rows_include_aggregate_members;aggregate_counts_are_groups;system_rows_are_independent;do_not_sum_incident_and_aggregate"}
	if queue.Status == "available" {
		bundle.Queue.Incident = exportQueueCounts(queue.Deliveries)
		bundle.Queue.Aggregate = exportQueueCounts(queue.Aggregates)
		bundle.Queue.System = exportQueueCounts(queue.System)
	}
	bundle.Jobs["host_retention"] = exportJob(s.store.RetentionDiagnostics())
	bundle.Jobs["http_retention"] = exportJob(s.store.HTTPRetentionDiagnostics())
	bundle.Jobs["network_retention"] = exportJob(s.store.NetworkRetentionDiagnostics())
	bundle.Jobs["manual_backup_generation"] = exportJob(s.backupJob.Snapshot())
	bundle.Workers["retention"] = diagnosticWorker{Status: "unavailable"}
	if s.retentionWorkerHealth != nil {
		r := s.retentionWorkerHealth()
		bundle.Workers["retention"] = diagnosticWorker{Status: exportEnum(r.Status, "never_started", "running", "not_running")}
	}
	bundle.Workers["notification"] = diagnosticWorker{Status: "unavailable"}
	bundle.Workers["network_retention"] = diagnosticWorker{Status: "unavailable"}
	if s.networkRetentionWorkerHealth != nil {
		r := s.networkRetentionWorkerHealth()
		bundle.Workers["network_retention"] = diagnosticWorker{Status: exportEnum(r.Status, "never_started", "running", "not_running")}
	}
	if s.alertHealth != nil {
		r := s.alertHealth()
		bundle.Jobs["notification_evaluation"] = exportJob(r.Evaluation)
		item := diagnosticWorker{Status: exportEnum(r.Status, "running", "not_running")}
		if r.Reason != "" {
			item.ErrorClass = exportEnum(r.Reason, "encryption_configuration_unavailable", "notification_origin_configuration_unavailable")
		}
		bundle.Workers["notification"] = item
	}
	bundle.Workers["latency_scheduler"] = diagnosticWorker{Status: "unavailable"}
	if s.schedulerHealth != nil {
		if r, ok := s.schedulerHealth().(scheduler.Snapshot); ok {
			status := "not_running"
			if r.Running {
				status = "running"
			}
			bundle.Workers["latency_scheduler"] = diagnosticWorker{Status: status}
			bundle.Jobs["latency_scheduler"] = exportJob(r.Job)
		}
	}
	bundle.Workers["http_scheduler"] = diagnosticWorker{Status: "unavailable"}
	if s.httpSchedulerHealth != nil {
		if r, ok := s.httpSchedulerHealth().(scheduler.HTTPSnapshot); ok {
			status := "not_running"
			if r.Running {
				status = "running"
			}
			bundle.Workers["http_scheduler"] = diagnosticWorker{Status: status}
			bundle.Jobs["http_scheduler"] = exportJob(r.Job)
		}
	}
	bundle.Workers["scheduled_backup"] = diagnosticWorker{Status: "unavailable"}
	if s.scheduledBackups != nil {
		r := s.scheduledBackups.Diagnostics()
		status := "not_running"
		if r.Running {
			status = "running"
		}
		item := diagnosticWorker{Status: status}
		if r.Operation != "" {
			item.Operation = exportEnum(r.Operation, "backup", "recovery")
		}
		if r.ErrorCode != "" {
			item.ErrorClass = "backup_operation_failed"
		}
		bundle.Workers["scheduled_backup"] = item
	}
	if ctx.Err() == nil {
		if transport, ok := s.gateway.TryDiagnostics(); ok {
			bundle.Transport.Status = "available"
			bundle.Transport.AgentConnections = exportInt(&transport.AgentConnections)
			bundle.Transport.V1Connections = exportInt(&transport.V1Connections)
			bundle.Transport.V2Connections = exportInt(&transport.V2Connections)
			bundle.Transport.UnknownConnections = exportInt(&transport.UnknownConnections)
			bundle.Transport.PendingResults = exportInt(&transport.PendingResults)
			bundle.Transport.ExpiredResults = exportInt(&transport.ExpiredResults)
			bundle.Transport.HTTPStatus = exportEnum(transport.HTTPResultsStatus, "available", "unavailable")
			if transport.HTTPResultsStatus == "available" {
				bundle.Transport.HTTPPending = exportInt(transport.HTTPPendingResults)
				bundle.Transport.HTTPExpired = exportInt(transport.HTTPExpiredResults)
			}
		}
		if count, ok := s.hub.TrySubscriberCount(); ok {
			bundle.Transport.BrowserSubscriptions = exportInt(&count)
		}
	}
	return bundle
}
