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

func (s *Server) publicNodeTimeline(c *gin.Context) { s.writeNodeTimeline(c, false) }
func (s *Server) adminNodeTimeline(c *gin.Context)  { s.writeNodeTimeline(c, true) }

func (s *Server) writeNodeTimeline(c *gin.Context, admin bool) {
	c.Header("Cache-Control", "no-store")
	query := c.Request.URL.Query()
	for _, key := range []string{"cursor", "limit"} {
		if len(query[key]) > 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "duplicate pagination parameter"})
			return
		}
	}
	window, err := parseHistoryWindow(query, time.Now().UTC())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	limit := 50
	if query.Has("limit") {
		limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || limit < 1 || limit > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 100"})
			return
		}
	}
	if query.Get("cursor") != "" && (!query.Has("start") || !query.Has("end")) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "use a valid fixed window when paging timeline events"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	page, err := s.store.NodeTimelineRange(ctx, c.Param("nodeID"), window.Start, window.End, admin, query.Get("cursor"), limit)
	switch {
	case errors.Is(err, store.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
	case errors.Is(err, store.ErrTimelineCursor):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read node timeline"})
	default:
		c.JSON(http.StatusOK, page)
	}
}
