package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

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

func (s *Server) listHTTPServices(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil || limit < 1 || limit > 100 || len(c.Query("after")) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid pagination"})
		return
	}
	items, next, err := s.store.ListHTTPServices(c.Request.Context(), c.Query("after"), limit)
	if err != nil {
		writeHTTPServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"services": items, "next_cursor": next, "execution_enabled": s.gateway.HTTPProbesEnabled()})
}

func (s *Server) deleteHTTPService(c *gin.Context) {
	revision, err := strconv.ParseInt(c.Query("revision"), 10, 64)
	if err != nil || revision <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "positive revision is required"})
		return
	}
	id := c.Param("serviceID")
	if err = s.store.DeleteHTTPService(c.Request.Context(), id, revision); err != nil {
		writeHTTPServiceError(c, err)
		return
	}
	s.audit(c, "delete", "http_service", id, gin.H{"revision": revision})
	c.Status(http.StatusNoContent)
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
