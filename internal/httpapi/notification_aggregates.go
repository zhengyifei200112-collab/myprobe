package httpapi

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

var notificationContextID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:\-]{0,127}$`)

func aggregatePageQuery(c *gin.Context) (int64, int, error) {
	before, limit := int64(0), 25
	values := c.Request.URL.Query()
	for _, key := range []string{"before", "limit"} {
		if raw, ok := values[key]; ok {
			if len(raw) != 1 {
				return 0, 0, store.ErrInvalidAggregateQuery
			}
			n, err := strconv.ParseInt(raw[0], 10, 64)
			if err != nil || (key == "before" && n < 0) || (key == "limit" && (n < 1 || n > 100)) {
				return 0, 0, store.ErrInvalidAggregateQuery
			}
			if key == "before" {
				before = n
			} else {
				limit = int(n)
			}
		}
	}
	return before, limit, nil
}

func (s *Server) notificationAggregates(c *gin.Context) {
	before, limit, err := aggregatePageQuery(c)
	if err != nil {
		aggregateContextError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	page, err := s.store.ListNotificationAggregates(ctx, before, limit)
	if err != nil {
		aggregateContextError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func aggregateContextError(c *gin.Context, err error) {
	status, code, msg := 500, "aggregate_unavailable", "unable to read notification aggregate"
	if errors.Is(err, store.ErrInvalidAggregateQuery) {
		status, code, msg = 400, "invalid_aggregate_query", "invalid aggregate query"
	}
	if errors.Is(err, store.ErrNotFound) {
		status, code, msg = 404, "aggregate_not_found", "aggregate does not exist"
	}
	c.JSON(status, gin.H{"code": code, "error": msg})
}
func (s *Server) notificationAggregate(c *gin.Context) {
	before, limit := 0, 25
	values := c.Request.URL.Query()
	for _, key := range []string{"before", "limit"} {
		if raw, ok := values[key]; ok {
			if len(raw) != 1 {
				aggregateContextError(c, store.ErrInvalidAggregateQuery)
				return
			}
			n, err := strconv.Atoi(raw[0])
			if err != nil || (key == "before" && n < 0) || (key == "limit" && (n < 1 || n > 100)) {
				aggregateContextError(c, store.ErrInvalidAggregateQuery)
				return
			}
			if key == "before" {
				before = n
			} else {
				limit = n
			}
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	page, err := s.store.AggregateContext(ctx, c.Param("aggregateID"), before, limit)
	if err != nil {
		aggregateContextError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}
func (s *Server) getIncident(c *gin.Context) {
	id := c.Param("incidentID")
	if !notificationContextID.MatchString(id) {
		c.JSON(400, gin.H{"code": "invalid_incident", "error": "invalid incident"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	item, err := s.store.Incident(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(404, gin.H{"code": "incident_not_found", "error": "incident does not exist"})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"code": "incident_unavailable", "error": "unable to read incident"})
		return
	}
	c.JSON(200, gin.H{"incident": item})
}
