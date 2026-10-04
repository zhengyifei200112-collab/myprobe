package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type httpServiceRequest struct {
	Revision        int64          `json:"revision"`
	Name            string         `json:"name"`
	Enabled         *bool          `json:"enabled"`
	IntervalSeconds int            `json:"interval_seconds"`
	Spec            httpcheck.Spec `json:"spec"`
	NodeIDs         []string       `json:"node_ids"`
}

func (s *Server) getHTTPService(c *gin.Context) {
	v, err := s.store.HTTPService(c.Request.Context(), c.Param("serviceID"))
	if err != nil {
		writeHTTPServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"service": v, "execution_enabled": s.gateway.HTTPProbesEnabled()})
}

func (s *Server) createHTTPService(c *gin.Context)  { s.saveHTTPService(c, true) }
func (s *Server) replaceHTTPService(c *gin.Context) { s.saveHTTPService(c, false) }

func (s *Server) saveHTTPService(c *gin.Context, create bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	defer c.Request.Body.Close()
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var request httpServiceRequest
	if err := decoder.Decode(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid service configuration"})
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || request.Enabled == nil || (create && request.Revision != 0) || (!create && request.Revision <= 0) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid service configuration"})
		return
	}
	v := store.HTTPService{Name: request.Name, Enabled: *request.Enabled, IntervalSeconds: request.IntervalSeconds, Spec: request.Spec, NodeIDs: request.NodeIDs, Revision: request.Revision}
	if !create {
		v.ID = c.Param("serviceID")
	}
	v, err := s.store.SaveHTTPService(c.Request.Context(), v)
	if err != nil {
		writeHTTPServiceError(c, err)
		return
	}
	action, status := "update", http.StatusOK
	if create {
		action, status = "create", http.StatusCreated
	}
	// Do not audit the target URL, assertion contents or raw request.
	s.audit(c, action, "http_service", v.ID, gin.H{"revision": v.Revision, "enabled": v.Enabled, "node_count": len(v.NodeIDs)})
	c.JSON(status, gin.H{"service": v, "execution_enabled": s.gateway.HTTPProbesEnabled()})
}

func writeHTTPServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, store.ErrServiceConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "service configuration changed; reload before saving"})
	case errors.Is(err, store.ErrInvalidHTTPService):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid service configuration or node selection"})
	case errors.Is(err, store.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "service storage operation failed"})
	}
}
