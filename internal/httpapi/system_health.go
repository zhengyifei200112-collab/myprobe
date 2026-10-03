package httpapi

import (
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *Server) systemHealth(c *gin.Context) {
	var schedulerHealth any = gin.H{"status": "unavailable", "reason": "observer_not_attached"}
	if s.schedulerHealth != nil {
		schedulerHealth = s.schedulerHealth()
	}
	c.JSON(http.StatusOK, gin.H{
		"server":      s.runtimeIdentity(time.Now()),
		"scheduler":   schedulerHealth,
		"observed_at": time.Now().UTC(),
		"database":    s.store.DatabaseDiagnostics(c.Request.Context()),
		"retention": gin.H{
			"observation_scope": "process",
			"job":               s.store.RetentionDiagnostics(),
		},
		"transport":             s.gateway.Diagnostics(),
		"browser_subscriptions": s.hub.SubscriberCount(),
		"notification_queue":    gin.H{"status": "unavailable", "reason": "durable_outbox_not_integrated"},
	})
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
