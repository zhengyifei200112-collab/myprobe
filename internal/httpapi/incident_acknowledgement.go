package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func acknowledgementError(c *gin.Context, err error) {
	status, code, message := http.StatusInternalServerError, "acknowledgement_unavailable", "unable to read or update acknowledgement"
	switch {
	case errors.Is(err, store.ErrInvalidAcknowledgement):
		status, code, message = http.StatusBadRequest, "invalid_acknowledgement", "invalid incident, request ID or note"
	case errors.Is(err, store.ErrAcknowledgementRequestConflict):
		status, code, message = http.StatusConflict, "acknowledgement_request_conflict", "request ID was used for different content or actor"
	case errors.Is(err, store.ErrIncidentAlreadyAcknowledged):
		status, code, message = http.StatusConflict, "incident_already_acknowledged", "incident already acknowledged; refresh to view the original record"
	case errors.Is(err, store.ErrIncidentNotFiring):
		status, code, message = http.StatusConflict, "incident_not_firing", "only a firing incident can be newly acknowledged"
	case errors.Is(err, store.ErrNotFound):
		status, code, message = http.StatusNotFound, "incident_not_found", "incident does not exist"
	}
	c.JSON(status, gin.H{"error": message, "code": code})
}

func (s *Server) acknowledgeIncident(c *gin.Context) {
	var request struct {
		RequestID string `json:"request_id"`
		Note      string `json:"note"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10))
	if err != nil || !utf8.Valid(body) {
		acknowledgementError(c, store.ErrInvalidAcknowledgement)
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		acknowledgementError(c, store.ErrInvalidAcknowledgement)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	session := c.MustGet("session").(store.Session)
	result, err := s.store.AcknowledgeIncident(ctx, c.Param("incidentID"), request.RequestID, request.Note, session.UserID)
	if err != nil {
		acknowledgementError(c, err)
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	c.JSON(status, result)
}

func (s *Server) incidentAcknowledgement(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	view, err := s.store.IncidentAcknowledgement(ctx, c.Param("incidentID"))
	if err != nil {
		acknowledgementError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}
