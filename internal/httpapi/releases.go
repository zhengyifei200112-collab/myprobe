package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/releasecheck"
)

// This DTO deliberately excludes node metadata, credentials and upstream bodies.
type releaseStatusResponse struct {
	ObservedAt       time.Time               `json:"observed_at"`
	Upstream         releasecheck.Snapshot   `json:"upstream"`
	Server           releasecheck.Comparison `json:"server"`
	Agent            *agentReleaseStatus     `json:"agent,omitempty"`
	DocumentationURL string                  `json:"documentation_url"`
}

type agentReleaseStatus struct {
	NodeID          string                  `json:"node_id"`
	Evidence        string                  `json:"evidence"`
	HelloReceivedAt *time.Time              `json:"hello_received_at"`
	Comparison      releasecheck.Comparison `json:"comparison"`
}

func (s *Server) releaseStatus(c *gin.Context) {
	query, err := parseReleaseQuery(c.Request.URL.RawQuery)
	// ParseQuery errors and duplicate/unknown keys must not silently change scope.
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_release_query"})
		return
	}
	var agent *agentReleaseStatus
	version := ""
	if id := query.Get("node_id"); id != "" {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		node, err := s.store.NodeDiagnostics(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node_not_found"})
			return
		}
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "node_evidence_unavailable"})
			return
		}
		version = node.AgentVersion
		agent = &agentReleaseStatus{NodeID: id, Evidence: "last_advertised", HelloReceivedAt: node.HelloReceivedAt}
		if node.HelloReceivedAt == nil {
			agent.Evidence = "not_advertised"
		}
	}
	snapshot := releasecheck.Snapshot{Status: "unavailable", Source: "github_public_release", ErrorCode: "checker_not_attached"}
	if s.releases != nil {
		if c.Request.Method == http.MethodPost {
			snapshot = s.releases.Refresh(c.Request.Context())
		} else {
			snapshot = s.releases.Snapshot()
		}
	}
	if agent != nil {
		agent.Comparison = releasecheck.Compare(version, snapshot)
	}
	c.JSON(http.StatusOK, releaseStatusResponse{ObservedAt: time.Now().UTC(), Upstream: snapshot, Server: releasecheck.Compare(s.buildVersion, snapshot), Agent: agent, DocumentationURL: releasecheck.DocumentationURL})
}

func parseReleaseQuery(raw string) (url.Values, error) {
	if len(raw) > 512 {
		return nil, errors.New("invalid release query")
	}
	query, err := url.ParseQuery(raw)
	if err != nil || len(query) > 1 {
		return nil, errors.New("invalid release query")
	}
	for key, values := range query {
		if key != "node_id" || len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 128 || strings.TrimSpace(values[0]) != values[0] {
			return nil, errors.New("invalid release query")
		}
	}
	return query, nil
}
