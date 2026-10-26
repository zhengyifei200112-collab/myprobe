package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func maintenanceContextPagination(c *gin.Context) (string, int, bool) {
	values := c.Request.URL.Query()
	limit := 25
	if raw, ok := values["limit"]; ok {
		if len(raw) != 1 {
			maintenanceContextError(c, store.ErrInvalidMaintenance)
			return "", 0, false
		}
		n, err := strconv.Atoi(raw[0])
		if err != nil || n < 1 || n > 100 {
			maintenanceContextError(c, store.ErrInvalidMaintenance)
			return "", 0, false
		}
		limit = n
	}
	after := ""
	if raw, ok := values["after"]; ok {
		if len(raw) != 1 || len(raw[0]) > 128 {
			maintenanceContextError(c, store.ErrInvalidMaintenance)
			return "", 0, false
		}
		after = raw[0]
	}
	return after, limit, true
}
func maintenanceContextError(c *gin.Context, err error) {
	status, message := http.StatusInternalServerError, "unable to read maintenance context"
	if errors.Is(err, store.ErrInvalidMaintenance) {
		status, message = http.StatusBadRequest, "invalid maintenance query"
	}
	if errors.Is(err, store.ErrNotFound) {
		status, message = http.StatusNotFound, "incident or delivery does not exist"
	}
	c.JSON(status, gin.H{"error": message})
}
func (s *Server) incidentMaintenance(c *gin.Context) {
	after, limit, ok := maintenanceContextPagination(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	page, err := s.store.ListIncidentMaintenance(ctx, c.Param("incidentID"), after, limit, time.Now().UTC())
	if err != nil {
		maintenanceContextError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}
func (s *Server) deliveryMaintenance(c *gin.Context) {
	after, limit, ok := maintenanceContextPagination(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	page, err := s.store.ListDeliveryMaintenance(ctx, c.Param("deliveryID"), after, limit)
	if err != nil {
		maintenanceContextError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}
