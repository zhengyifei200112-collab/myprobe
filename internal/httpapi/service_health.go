package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/servicehealth"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type serviceHealthListItem struct {
	store.HTTPServiceSummary
	Health servicehealth.Summary `json:"health"`
}
type serviceObserverHealth struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	servicehealth.Assessment
	TaskID      string     `json:"task_id,omitempty"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	StatusCode  int        `json:"status_code,omitempty"`
	DurationMS  *float64   `json:"duration_ms,omitempty"`
}

func healthNodeIDs(snapshots []store.HTTPHealthSnapshot) []string {
	ids := []string{}
	seen := map[string]bool{}
	for _, snapshot := range snapshots {
		for _, observer := range snapshot.Observers {
			if !seen[observer.NodeID] {
				seen[observer.NodeID] = true
				ids = append(ids, observer.NodeID)
			}
		}
	}
	return ids
}

func assessServiceSnapshot(snapshot store.HTTPHealthSnapshot, enabled bool, available map[string]bool, now time.Time) (servicehealth.Summary, []serviceObserverHealth) {
	observers := make([]serviceObserverHealth, 0, len(snapshot.Observers))
	assessments := make([]servicehealth.Assessment, 0, len(snapshot.Observers))
	for _, observer := range snapshot.Observers {
		input := servicehealth.Input{ServiceEnabled: snapshot.Service.Enabled, ExecutionEnabled: enabled, AgentAvailable: available[observer.NodeID], LastSeenAt: observer.LastSeenAt, ReportSeconds: observer.ReportSeconds, Capabilities: observer.Capabilities, IntervalSeconds: snapshot.Service.IntervalSeconds}
		if o := observer.Observation; o != nil {
			input.Observation = &servicehealth.Observation{ScheduledAt: o.ScheduledAt, ExpiresAt: o.ExpiresAt, Result: o.Result}
		}
		assessment := servicehealth.Evaluate(input, now)
		item := serviceObserverHealth{NodeID: observer.NodeID, NodeName: observer.NodeName, Assessment: assessment}
		if o := observer.Observation; o != nil {
			item.TaskID = o.TaskID
			scheduled := o.ScheduledAt
			item.ScheduledAt = &scheduled
			if r := o.Result; r != nil {
				completed := r.CompletedAt
				item.CompletedAt = &completed
				if assessment.Status != "unknown" {
					item.StatusCode = r.StatusCode
					duration := r.DurationMS
					item.DurationMS = &duration
				}
			}
		}
		observers = append(observers, item)
		assessments = append(assessments, assessment)
	}
	return servicehealth.Summarize(assessments, now), observers
}

func (s *Server) httpServiceHealth(c *gin.Context) {
	if len(c.Request.URL.Query()) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "health snapshots do not accept query parameters"})
		return
	}
	now := time.Now().UTC()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	snapshots, err := s.store.HTTPHealthSnapshots(ctx, []string{c.Param("serviceID")}, now)
	if err != nil {
		writeHTTPServiceError(c, err)
		return
	}
	if len(snapshots) != 1 {
		writeHTTPServiceError(c, store.ErrNotFound)
		return
	}
	enabled, available := s.gateway.HTTPAvailability(healthNodeIDs(snapshots))
	summary, observers := assessServiceSnapshot(snapshots[0], enabled, available, now)
	c.JSON(http.StatusOK, gin.H{"service": snapshots[0].Service, "health": summary, "observers": observers, "execution_enabled": enabled})
}
