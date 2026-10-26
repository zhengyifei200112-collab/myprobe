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

func (s *Server) createMaintenanceWindow(c *gin.Context) {
	var request struct {
		Draft         store.MaintenanceDraft `json:"draft"`
		PreviewDigest string                 `json:"preview_digest"`
		RequestID     string                 `json:"request_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		maintenanceMutationError(c, store.ErrInvalidMaintenance)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	session := c.MustGet("session").(store.Session)
	result, err := s.store.CreateConfirmedMaintenance(ctx, request.Draft, request.PreviewDigest, request.RequestID, session.UserID)
	if err != nil {
		maintenanceMutationError(c, err)
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	c.JSON(status, result)
}

func (s *Server) cancelMaintenanceWindow(c *gin.Context) {
	var request struct {
		Revision int64 `json:"revision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		maintenanceMutationError(c, store.ErrInvalidMaintenance)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	session := c.MustGet("session").(store.Session)
	result, err := s.store.CancelConfirmedMaintenance(ctx, c.Param("windowID"), request.Revision, session.UserID, time.Now().UTC())
	if err != nil {
		maintenanceMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func maintenanceMutationError(c *gin.Context, err error) {
	status, code, message := http.StatusInternalServerError, "maintenance_unavailable", "unable to update maintenance window"
	switch {
	case errors.Is(err, store.ErrInvalidMaintenance):
		status, code, message = http.StatusBadRequest, "invalid_maintenance", "invalid maintenance fields or selection"
	case errors.Is(err, store.ErrMaintenancePreviewChanged):
		status, code, message = http.StatusConflict, "maintenance_preview_changed", "maintenance content or members changed; preview again"
	case errors.Is(err, store.ErrMaintenanceRequestConflict):
		status, code, message = http.StatusConflict, "maintenance_request_conflict", "request ID was used for different content"
	case errors.Is(err, store.ErrMaintenanceConflict):
		status, code, message = http.StatusConflict, "maintenance_conflict", "maintenance window changed or was canceled; refresh"
	case errors.Is(err, store.ErrMaintenanceExpired):
		status, code, message = http.StatusConflict, "maintenance_expired", "maintenance window has ended"
	case errors.Is(err, store.ErrMaintenanceLimit):
		status, code, message = http.StatusConflict, "maintenance_limit", "maintenance capacity limit reached"
	case errors.Is(err, store.ErrNotFound):
		status, code, message = http.StatusNotFound, "maintenance_not_found", "maintenance window does not exist"
	}
	c.JSON(status, gin.H{"error": message, "code": code})
}

func (s *Server) previewMaintenanceWindow(c *gin.Context) {
	var draft store.MaintenanceDraft
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&draft) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid maintenance draft", "code": "invalid_maintenance_draft"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	preview, err := s.store.PreviewMaintenanceWindow(ctx, draft)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrInvalidMaintenance):
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid maintenance fields or selection", "code": "invalid_maintenance_draft"})
		case errors.Is(err, store.ErrMaintenanceLimit):
			c.JSON(http.StatusBadRequest, gin.H{"error": "selection exceeds 100 nodes", "code": "maintenance_member_limit"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to preview maintenance window"})
		}
		return
	}
	c.JSON(http.StatusOK, preview)
}

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
