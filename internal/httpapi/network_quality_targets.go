package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	quality "github.com/zhengyifei200112-collab/myprobe/internal/networkquality"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type qualityTargetDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func (s *Server) publicNetworkQualityTargets(c *gin.Context) {
	s.writeNetworkQualityTargets(c, "", false)
}
func (s *Server) adminNetworkQualityTargets(c *gin.Context) {
	s.writeNetworkQualityTargets(c, "", true)
}
func (s *Server) shareNetworkQualityTargets(c *gin.Context) {
	item, ok := s.requireShare(c)
	if !ok {
		return
	}
	s.writeNetworkQualityTargets(c, item.ID, false)
}

func parseQualityTargetQuery(raw string) ([]string, string, error) {
	if len(raw) > 4096 {
		return nil, "", quality.ErrInvalid
	}
	v, err := url.ParseQuery(raw)
	if err != nil {
		return nil, "", quality.ErrInvalid
	}
	for k, values := range v {
		if k != "node_id" && k != "cursor" {
			return nil, "", quality.ErrInvalid
		}
		if k == "cursor" && (len(values) != 1 || len(values[0]) > 512) {
			return nil, "", quality.ErrInvalid
		}
	}
	if len(v["node_id"]) < 1 || len(v["node_id"]) > 8 {
		return nil, "", quality.ErrInvalid
	}
	return v["node_id"], v.Get("cursor"), nil
}
func (s *Server) writeNetworkQualityTargets(c *gin.Context, share string, admin bool) {
	ids, cursor, err := parseQualityTargetQuery(c.Request.URL.RawQuery)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid network quality target query"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	var page store.NetworkQualityTargetsPage
	switch {
	case share != "":
		page, err = s.store.SharedNetworkQualityTargets(ctx, share, ids, cursor)
	case admin:
		page, err = s.store.AdminNetworkQualityTargets(ctx, ids, cursor)
	default:
		page, err = s.store.PublicNetworkQualityTargets(ctx, ids, cursor)
	}
	if err != nil {
		status, message := http.StatusInternalServerError, "failed to read network quality targets"
		switch {
		case errors.Is(err, quality.ErrInvalid):
			status, message = 400, "invalid network quality target query"
		case errors.Is(err, store.ErrNotFound):
			status, message = 404, "network quality not found"
		case errors.Is(err, store.ErrNetworkQualityUnavailable) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
			status, message = 503, "network quality temporarily unavailable"
		}
		c.JSON(status, gin.H{"error": message})
		return
	}
	targets := make([]qualityTargetDTO, 0, len(page.Targets))
	for _, t := range page.Targets {
		targets = append(targets, qualityTargetDTO{ID: t.ID, Name: t.Name, Kind: t.Kind})
	}
	c.JSON(200, gin.H{"targets": targets, "next_cursor": page.NextCursor, "page_size": store.NetworkQualityTargetPageSize, "observed_at": time.Now().UTC()})
}
