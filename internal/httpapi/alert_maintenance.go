package httpapi

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

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
