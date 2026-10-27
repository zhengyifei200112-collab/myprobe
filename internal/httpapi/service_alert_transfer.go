package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func serviceAlertTransferError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, store.ErrServiceAlertImportInvalid), errors.Is(err, store.ErrInvalidHTTPService):
		c.JSON(400, gin.H{"error": "invalid service alert document or mapping", "code": "service_alert_import_invalid"})
	case errors.Is(err, store.ErrServiceAlertImportConflict), errors.Is(err, store.ErrServiceConflict):
		c.JSON(409, gin.H{"error": "service alert content or destination changed; preview again", "code": "service_alert_import_preview_changed"})
	case errors.Is(err, store.ErrServiceAlertImportRequestConflict):
		c.JSON(409, gin.H{"error": "request ID was used for different content", "code": "service_alert_import_request_conflict"})
	case errors.Is(err, store.ErrServiceAlertImportLimit):
		c.JSON(409, gin.H{"error": "service alert import capacity reached", "code": "service_alert_import_limit"})
	default:
		c.JSON(500, gin.H{"error": "service alert transfer unavailable"})
	}
}
func noServiceTransferQuery(c *gin.Context) bool {
	if c.Request.URL.RawQuery != "" {
		serviceAlertTransferError(c, store.ErrServiceAlertImportInvalid)
		return false
	}
	return true
}
func (s *Server) exportServiceAlerts(c *gin.Context) {
	if !noServiceTransferQuery(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	b, err := s.store.ExportServiceAlerts(ctx)
	if err != nil {
		serviceAlertTransferError(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="myprobe-service-alerts.json"`)
	c.JSON(200, b)
}
func (s *Server) serviceAlertDestinations(c *gin.Context) {
	if !noServiceTransferQuery(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	v, err := s.store.ServiceAlertDestinations(ctx)
	if err != nil {
		serviceAlertTransferError(c, err)
		return
	}
	c.JSON(200, gin.H{"services": v.Services, "channels": v.Channels, "execution_enabled": s.gateway.HTTPProbesEnabled()})
}

type serviceAlertImportRequest struct {
	Bundle        store.ServiceAlertBundle        `json:"bundle"`
	Mapping       store.ServiceAlertImportMapping `json:"mapping"`
	RequestID     string                          `json:"request_id,omitempty"`
	PreviewDigest string                          `json:"preview_digest,omitempty"`
}

func decodeServiceAlertImport(c *gin.Context, value any) bool {
	if !noServiceTransferQuery(c) {
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || !errors.Is(d.Decode(new(any)), io.EOF) {
		serviceAlertTransferError(c, store.ErrServiceAlertImportInvalid)
		return false
	}
	return true
}
func (s *Server) previewServiceAlertImport(c *gin.Context) {
	var r struct {
		Bundle  store.ServiceAlertBundle        `json:"bundle"`
		Mapping store.ServiceAlertImportMapping `json:"mapping"`
	}
	if !decodeServiceAlertImport(c, &r) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	p, err := s.store.PreviewServiceAlertImport(ctx, r.Bundle, r.Mapping)
	if err != nil {
		serviceAlertTransferError(c, err)
		return
	}
	c.JSON(200, gin.H{"preview": p, "execution_enabled": s.gateway.HTTPProbesEnabled()})
}
func (s *Server) applyServiceAlertImport(c *gin.Context) {
	var r serviceAlertImportRequest
	if !decodeServiceAlertImport(c, &r) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	result, err := s.store.ApplyServiceAlertImport(ctx, r.RequestID, r.Bundle, r.Mapping, r.PreviewDigest)
	if err != nil {
		serviceAlertTransferError(c, err)
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	} else {
		s.audit(c, "import", "service_alerts", r.RequestID, gin.H{"service_ids": result.ServiceIDs, "created": result.Created, "updated": result.Updated})
	}
	c.JSON(status, result)
}
