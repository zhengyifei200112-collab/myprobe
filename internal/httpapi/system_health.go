package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *Server) nodeDiagnostics(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	node, err := s.store.NodeDiagnostics(ctx, c.Param("nodeID"))
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node diagnostics"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"observed_at": time.Now().UTC(), "node": node,
		"websocket_connected":           s.gateway.NodeConnected(node.NodeID),
		"configuration_acknowledgement": gin.H{"status": "unavailable", "reason": "acknowledgement_not_integrated"},
		"last_report_transport":         "unknown",
	})
}

func (s *Server) systemHealth(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	var engine any = gin.H{"status": "unavailable", "reason": "observer_not_attached"}
	if s.alertHealth != nil {
		engine = s.alertHealth()
	}
	var schedulerHealth any = gin.H{"status": "unavailable", "reason": "observer_not_attached"}
	if s.schedulerHealth != nil {
		schedulerHealth = s.schedulerHealth()
	}
	var httpSchedulerHealth any = gin.H{"status": "unavailable", "reason": "observer_not_attached"}
	if s.httpSchedulerHealth != nil {
		httpSchedulerHealth = s.httpSchedulerHealth()
	}
	var retentionWorker any = gin.H{"status": "unavailable", "reason": "observer_not_attached"}
	if s.retentionWorkerHealth != nil {
		retentionWorker = s.retentionWorkerHealth()
	}
	c.JSON(http.StatusOK, gin.H{
		"notification_engine": engine,
		"backup": gin.H{
			"observation_scope": "process", "operation": "encrypted_file_generation",
			"job": s.backupJob.Snapshot(), "download_saved": "unknown", "recovery_verified": "unknown",
		},
		"server":                 s.runtimeIdentity(time.Now()),
		"scheduler":              schedulerHealth,
		"http_scheduler":         httpSchedulerHealth,
		"http_execution_enabled": s.gateway.HTTPProbesEnabled(),
		"observed_at":            time.Now().UTC(),
		"database":               s.store.DatabaseDiagnostics(ctx),
		"retention": gin.H{
			"configuration":       s.retentionConfiguration(),
			"observation_scope":   "process",
			"job":                 s.store.RetentionDiagnostics(),
			"http_catchup_job":    s.store.HTTPRetentionDiagnostics(),
			"network_catchup_job": s.store.NetworkRetentionDiagnostics(),
			"network_worker":      s.networkRetentionWorkerSnapshot(),
			"worker":              retentionWorker,
		},
		"transport":             s.gateway.Diagnostics(),
		"browser_subscriptions": s.hub.SubscriberCount(),
		"notification_queue":    s.store.NotificationQueueDiagnostics(ctx, time.Now().UTC()),
	})
}

func (s *Server) networkRetentionWorkerSnapshot() any {
	if s.networkRetentionWorkerHealth == nil {
		return map[string]string{"status": "not_attached"}
	}
	return s.networkRetentionWorkerHealth()
}

func (s *Server) retentionConfiguration() gin.H {
	r := s.config.Retention
	if r.Raw <= 0 || r.OneMinute <= 0 || r.FiveMinute <= 0 || r.Interval <= 0 {
		return gin.H{"status": "unavailable"}
	}
	return gin.H{"status": "available", "raw_seconds": r.Raw.Seconds(),
		"one_minute_seconds": r.OneMinute.Seconds(), "five_minute_seconds": r.FiveMinute.Seconds(),
		"run_interval_seconds": r.Interval.Seconds()}
}

func (s *Server) runtimeIdentity(now time.Time) gin.H {
	result := gin.H{"version_status": "unavailable", "uptime_status": "unavailable",
		"go_version": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH}
	if s.buildVersion != "" {
		result["version_status"], result["version"] = "available", s.buildVersion
	}
	if !s.processStartedAt.IsZero() && !s.processStartedAt.After(now) {
		result["uptime_status"] = "available"
		result["started_at"] = s.processStartedAt.UTC()
		result["uptime_seconds"] = now.Sub(s.processStartedAt).Seconds()
	}
	return result
}
