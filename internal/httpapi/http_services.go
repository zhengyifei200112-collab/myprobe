package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

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

func (s *Server) httpServiceStatistics(c *gin.Context) {
	values := c.Request.URL.Query()
	for _, key := range []string{"start", "end", "node_id", "exclude_maintenance"} {
		if len(values[key]) > 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repeated statistics parameter"})
			return
		}
	}
	exclude := false
	if value, present := values["exclude_maintenance"]; present {
		if len(value) != 1 || (value[0] != "true" && value[0] != "false") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "exclude_maintenance must be true or false"})
			return
		}
		exclude = value[0] == "true"
	}
	start, startErr := time.Parse(time.RFC3339Nano, c.Query("start"))
	end, endErr := time.Parse(time.RFC3339Nano, c.Query("end"))
	nodeID := c.Query("node_id")
	if startErr != nil || endErr != nil || nodeID == "" || len(nodeID) > 128 || !end.After(start) || end.Sub(start) > 31*24*time.Hour || start.Year() < 1970 || end.Year() > 2100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "node_id and a valid start/end range of at most 31 days are required"})
		return
	}
	now := time.Now().UTC()
	queryCtx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	stats, err := s.store.HTTPServiceStatistics(queryCtx, c.Param("serviceID"), nodeID, start, end, now, exclude)
	if err != nil {
		writeHTTPServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"statistics": stats, "node_id": nodeID, "server_time": now, "maintenance_excluded": stats.MaintenanceExcluded, "execution_enabled": s.gateway.HTTPProbesEnabled()})
}

func (s *Server) recentHTTPResults(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil || limit < 1 || limit > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be 1 to 100"})
		return
	}
	items, err := s.store.RecentHTTPResults(c.Request.Context(), c.Param("serviceID"), limit)
	if err != nil {
		writeHTTPServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"observations": items, "execution_enabled": s.gateway.HTTPProbesEnabled(), "server_time": time.Now().UTC()})
}

func (s *Server) listHTTPServices(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil || limit < 1 || limit > 100 || len(c.Query("after")) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid pagination"})
		return
	}
	now := time.Now().UTC()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	items, next, err := s.store.ListHTTPServices(ctx, c.Query("after"), limit)
	if err != nil {
		writeHTTPServiceError(c, err)
		return
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	snapshots, err := s.store.HTTPHealthSnapshots(ctx, ids, now)
	if err != nil {
		writeHTTPServiceError(c, err)
		return
	}
	enabled, available := s.gateway.HTTPAvailability(healthNodeIDs(snapshots))
	visible := make([]serviceHealthListItem, 0, len(snapshots))
	for _, snapshot := range snapshots {
		summary, _ := assessServiceSnapshot(snapshot, enabled, available, now)
		visible = append(visible, serviceHealthListItem{HTTPServiceSummary: snapshot.Service, Health: summary})
	}
	c.JSON(http.StatusOK, gin.H{"services": visible, "next_cursor": next, "execution_enabled": enabled})
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
