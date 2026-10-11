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
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

// previewMaintenanceMembers resolves scope only. It neither reserves membership
// nor authorizes a later creation without transactional revalidation.
func (s *Server) previewMaintenanceMembers(c *gin.Context) {
	var request struct {
		Selection store.MaintenanceSelection `json:"selection"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid maintenance selection", "code": "invalid_maintenance_selection"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	members, err := s.store.PreviewMaintenanceMembers(ctx, request.Selection)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrInvalidMaintenance):
			c.JSON(http.StatusBadRequest, gin.H{"error": "select existing nodes or matching tags", "code": "invalid_maintenance_selection"})
		case errors.Is(err, store.ErrMaintenanceLimit):
			c.JSON(http.StatusBadRequest, gin.H{"error": "selection exceeds 100 nodes", "code": "maintenance_member_limit"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to preview maintenance selection"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"selection": request.Selection, "members": members, "member_count": len(members)})
}

func (s *Server) listMaintenanceWindows(c *gin.Context) {
	limit := 25
	if values, ok := c.Request.URL.Query()["limit"]; ok {
		if len(values) != 1 {
			c.JSON(400, gin.H{"error": "limit must be specified once"})
			return
		}
		value, err := strconv.Atoi(values[0])
		if err != nil || value < 1 || value > 100 {
			c.JSON(400, gin.H{"error": "limit must be 1 through 100"})
			return
		}
		limit = value
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	items, next, err := s.store.ListMaintenanceWindows(ctx, c.Query("after"), limit)
	if err != nil {
		if errors.Is(err, store.ErrInvalidMaintenance) {
			c.JSON(400, gin.H{"error": "invalid maintenance query"})
		} else {
			c.JSON(500, gin.H{"error": "unable to read maintenance windows"})
		}
		return
	}
	c.JSON(200, gin.H{"windows": items, "next_cursor": next})
}
