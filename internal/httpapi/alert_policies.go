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
	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func policyError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, store.ErrInvalidAlertPolicy):
		c.JSON(400, gin.H{"error": "invalid alert policy configuration"})
	case errors.Is(err, store.ErrAlertPolicyConflict):
		c.JSON(409, gin.H{"error": "policy changed or conflicts with an enabled policy; reload before saving"})
	case errors.Is(err, store.ErrNotFound):
		c.JSON(404, gin.H{"error": "policy or node not found"})
	default:
		c.JSON(500, gin.H{"error": "unable to access alert policies"})
	}
}

func (s *Server) listAlertPolicies(c *gin.Context) {
	limit := 50
	if raw, ok := c.GetQuery("limit"); ok {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			c.JSON(400, gin.H{"error": "limit must be 1 through 100"})
			return
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	items, err := s.store.ListAlertPolicies(ctx)
	if err != nil {
		policyError(c, err)
		return
	}
	page := make([]store.AlertPolicy, 0, limit)
	next := ""
	for _, item := range items {
		if item.ID <= c.Query("after") {
			continue
		}
		if len(page) == limit {
			next = page[len(page)-1].ID
			break
		}
		page = append(page, item)
	}
	c.JSON(200, gin.H{"policies": page, "next_cursor": next, "evaluation_enabled": true, "evaluation": s.alerts.PolicyEvaluationStatus()})
}

func (s *Server) getAlertPolicy(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	items, err := s.store.ListAlertPolicies(ctx)
	if err != nil {
		policyError(c, err)
		return
	}
	for _, item := range items {
		if item.ID == c.Param("policyID") {
			c.JSON(200, gin.H{"policy": item, "evaluation_enabled": true, "evaluation": s.alerts.PolicyEvaluationStatus()})
			return
		}
	}
	policyError(c, store.ErrNotFound)
}

func (s *Server) effectiveAlertPolicies(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	items, err := s.store.EffectiveAlertPolicies(ctx, c.Param("nodeID"))
	if err != nil {
		policyError(c, err)
		return
	}
	c.JSON(200, gin.H{"decisions": items, "evaluation_enabled": true, "evaluation": s.alerts.PolicyEvaluationStatus()})
}

func (s *Server) saveAlertPolicy(c *gin.Context) {
	var request struct {
		Name            string            `json:"name"`
		Key             string            `json:"policy_key"`
		Enabled         *bool             `json:"enabled"`
		Priority        int               `json:"priority"`
		Scope           alertpolicy.Scope `json:"scope"`
		Revision        int64             `json:"revision"`
		Kind            string            `json:"kind"`
		ChannelID       string            `json:"channel_id"`
		Config          json.RawMessage   `json:"config"`
		CooldownSeconds int               `json:"cooldown_seconds"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || request.Enabled == nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		policyError(c, store.ErrInvalidAlertPolicy)
		return
	}
	creating := c.Request.Method == http.MethodPost
	if (creating && request.Revision != 0) || (!creating && request.Revision <= 0) {
		policyError(c, store.ErrInvalidAlertPolicy)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	p := store.AlertPolicy{Policy: alertpolicy.Policy{ID: c.Param("policyID"), Key: request.Key, Enabled: *request.Enabled, Priority: request.Priority, Scope: request.Scope}, Name: request.Name, Revision: request.Revision, Kind: request.Kind, ChannelID: request.ChannelID, Config: request.Config, CooldownSeconds: request.CooldownSeconds}
	saved, err := s.alerts.SavePolicy(ctx, p)
	if err != nil {
		policyError(c, err)
		return
	}
	action, status := "update", 200
	if creating {
		action, status = "create", 201
	}
	s.audit(c, action, "alert_policy", saved.ID, gin.H{"revision": saved.Revision, "enabled": saved.Enabled, "scope_kind": saved.Scope.Kind})
	c.JSON(status, gin.H{"policy": saved, "evaluation_enabled": true, "evaluation": s.alerts.PolicyEvaluationStatus()})
}

func (s *Server) deleteAlertPolicy(c *gin.Context) {
	revision, err := strconv.ParseInt(c.Query("revision"), 10, 64)
	if err != nil || revision <= 0 {
		policyError(c, store.ErrInvalidAlertPolicy)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err = s.store.DeleteAlertPolicy(ctx, c.Param("policyID"), revision); err != nil {
		policyError(c, err)
		return
	}
	s.audit(c, "delete", "alert_policy", c.Param("policyID"), gin.H{"revision": revision})
	c.Status(http.StatusNoContent)
}
