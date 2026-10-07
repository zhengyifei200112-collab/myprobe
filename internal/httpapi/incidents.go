package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func (s *Server) deliveryAttempts(c *gin.Context) {
	id := c.Param("deliveryID")
	if len(id) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid delivery ID"})
		return
	}
	items, err := s.store.ListDeliveryAttempts(c.Request.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "delivery not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list attempts"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"attempts": items})
}

func incidentPage(c *gin.Context) (int64, int, bool) {
	before, err := strconv.ParseInt(c.DefaultQuery("before", "0"), 10, 64)
	limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil || limitErr != nil || before < 0 || limit < 1 || limit > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid pagination"})
		return 0, 0, false
	}
	return before, limit, true
}

func (s *Server) listIncidents(c *gin.Context) {
	before, limit, ok := incidentPage(c)
	if !ok {
		return
	}
	state, node := c.Query("state"), c.Query("node_id")
	if (state != "" && state != "pending" && state != "firing" && state != "resolved") || len(node) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid incident filter"})
		return
	}
	items, err := s.store.ListIncidents(c.Request.Context(), store.IncidentFilter{Before: before, Limit: limit, State: state, NodeID: node})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list incidents"})
		return
	}
	var next int64
	if len(items) == limit {
		next = items[len(items)-1].Seq
	}
	c.JSON(http.StatusOK, gin.H{"incidents": items, "next_before": next})
}

func (s *Server) incidentDeliveries(c *gin.Context) {
	before, limit, ok := incidentPage(c)
	if !ok {
		return
	}
	id := c.Param("incidentID")
	if len(id) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid incident ID"})
		return
	}
	_, err := s.store.Incident(c.Request.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "incident not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read incident"})
		return
	}
	items, err := s.store.ListIncidentDeliveries(c.Request.Context(), id, before, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list deliveries"})
		return
	}
	var next int64
	if len(items) == limit {
		next = items[len(items)-1].Seq
	}
	c.JSON(http.StatusOK, gin.H{"deliveries": items, "next_before": next})
}
