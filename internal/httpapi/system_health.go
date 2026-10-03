package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *Server) systemHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
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
