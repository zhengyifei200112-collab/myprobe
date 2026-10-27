package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/notificationlink"
)

func (s *Server) httpServiceDiagnostics(c *gin.Context) {
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid diagnostics query"})
		return
	}
	allowed := map[string]bool{"node_id": true, "start": true, "end": true, "limit": true, "after": true}
	for key, items := range values {
		if !allowed[key] || len(items) != 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid diagnostics query"})
			return
		}
	}
	start, startErr := time.Parse(time.RFC3339Nano, c.Query("start"))
	end, endErr := time.Parse(time.RFC3339Nano, c.Query("end"))
	limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if startErr != nil || endErr != nil || limitErr != nil || limit < 1 || limit > 100 || !notificationlink.ValidDiagnosticContext(c.Param("serviceID"), c.Query("node_id"), start, end) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a service, observer and valid range of at most 31 days are required"})
		return
	}
	now := time.Now().UTC()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	data, err := s.store.HTTPServiceDiagnostics(ctx, c.Param("serviceID"), c.Query("node_id"), start, end, now, limit, c.Query("after"))
	if err != nil {
		writeHTTPServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"service": data.Service, "observer": data.Observer, "statistics": data.Statistics, "observations": data.Observations, "next_cursor": data.NextCursor, "server_time": now, "execution_enabled": s.gateway.HTTPProbesEnabled()})
}
