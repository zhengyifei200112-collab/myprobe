package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func (s *Server) getServiceAlerting(c *gin.Context) {
	settings, err := s.store.ServiceAlertSettings(c.Request.Context(), c.Param("serviceID"))
	if err != nil {
		writeHTTPServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"alerting": settings, "execution_enabled": s.config.HTTPProbesEnabled})
}

func (s *Server) serviceIncidents(c *gin.Context) {
	before, limit, err := aggregatePageQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid incident pagination"})
		return
	}
	if _, err := s.store.HTTPService(c.Request.Context(), c.Param("serviceID")); err != nil {
		writeHTTPServiceError(c, err)
		return
	}
	items, err := s.store.ListIncidents(c.Request.Context(), store.IncidentFilter{ServiceID: c.Param("serviceID"), Before: before, Limit: limit})
	if err != nil {
		writeHTTPServiceError(c, err)
		return
	}
	var next int64
	if len(items) == limit {
		next = items[len(items)-1].Seq
	}
	c.JSON(http.StatusOK, gin.H{"incidents": items, "next_before": next})
}
func (s *Server) putServiceAlerting(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	defer c.Request.Body.Close()
	var request struct {
		ServiceRevision int64  `json:"service_revision"`
		Revision        int64  `json:"revision"`
		ChannelID       string `json:"channel_id"`
		Enabled         *bool  `json:"enabled"`
		DurationSeconds int    `json:"duration_seconds"`
		RecoverySeconds int    `json:"recovery_seconds"`
		RepeatSeconds   int    `json:"repeat_seconds"`
		CooldownSeconds int    `json:"cooldown_seconds"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&request) != nil || decoder.Decode(&extra) != io.EOF || request.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid service alert configuration"})
		return
	}
	settings, err := s.store.SaveServiceAlertSettings(c.Request.Context(), store.ServiceAlertSettings{ServiceID: c.Param("serviceID"), ServiceRevision: request.ServiceRevision, Revision: request.Revision, ChannelID: request.ChannelID, Enabled: *request.Enabled, DurationSeconds: request.DurationSeconds, RecoverySeconds: request.RecoverySeconds, RepeatSeconds: request.RepeatSeconds, CooldownSeconds: request.CooldownSeconds})
	if err != nil {
		writeHTTPServiceError(c, err)
		return
	}
	s.audit(c, "update", "service_alerting", settings.ServiceID, gin.H{"revision": settings.Revision, "enabled": settings.Enabled})
	c.JSON(http.StatusOK, gin.H{"alerting": settings, "execution_enabled": s.config.HTTPProbesEnabled})
}
