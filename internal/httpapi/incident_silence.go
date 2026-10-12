package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func silenceError(c *gin.Context, err error) {
	status, code, message := 500, "silence_unavailable", "unable to read or update silence"
	switch {
	case errors.Is(err, store.ErrInvalidSilence):
		status, code, message = 400, "invalid_silence", "invalid silence request or query"
	case errors.Is(err, store.ErrSilenceRequestConflict):
		status, code, message = 409, "silence_request_conflict", "request ID was used for different content or actor"
	case errors.Is(err, store.ErrSilenceActive):
		status, code, message = 409, "silence_active", "incident already has an effective silence"
	case errors.Is(err, store.ErrSilenceConflict):
		status, code, message = 409, "silence_changed", "silence or incident changed; refresh before a new action"
	case errors.Is(err, store.ErrIncidentNotFiring):
		status, code, message = 409, "incident_not_firing", "only firing incidents accept new silence"
	case errors.Is(err, store.ErrNotFound):
		status, code, message = 404, "silence_not_found", "incident, silence or delivery does not exist"
	}
	c.JSON(status, gin.H{"code": code, "error": message})
}
func decodeSilenceRequest(c *gin.Context, target any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10))
	if err != nil || !utf8.Valid(body) {
		silenceError(c, store.ErrInvalidSilence)
		return false
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil || !errors.Is(d.Decode(new(any)), io.EOF) {
		silenceError(c, store.ErrInvalidSilence)
		return false
	}
	return true
}
func (s *Server) createIncidentSilence(c *gin.Context) {
	var request struct {
		RequestID string `json:"request_id"`
		store.CreateIncidentSilenceParams
	}
	if !decodeSilenceRequest(c, &request) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	result, err := s.store.CreateIncidentSilence(ctx, c.Param("incidentID"), request.RequestID, c.MustGet("session").(store.Session).UserID, request.CreateIncidentSilenceParams, time.Now().UTC())
	if err != nil {
		silenceError(c, err)
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	c.JSON(status, result)
}
func (s *Server) cancelIncidentSilence(c *gin.Context) {
	var request struct {
		RequestID string `json:"request_id"`
		Revision  int64  `json:"revision"`
	}
	if !decodeSilenceRequest(c, &request) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	result, err := s.store.CancelIncidentSilence(ctx, c.Param("incidentID"), c.Param("silenceID"), request.RequestID, c.MustGet("session").(store.Session).UserID, request.Revision, time.Now().UTC())
	if err != nil {
		silenceError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
func silencePagination(c *gin.Context) (string, int, bool) {
	values := c.Request.URL.Query()
	limit := 25
	after := ""
	if raw, ok := values["limit"]; ok {
		if len(raw) != 1 {
			silenceError(c, store.ErrInvalidSilence)
			return "", 0, false
		}
		n, err := strconv.Atoi(raw[0])
		if err != nil || n < 1 || n > 100 {
			silenceError(c, store.ErrInvalidSilence)
			return "", 0, false
		}
		limit = n
	}
	if raw, ok := values["after"]; ok {
		if len(raw) != 1 || len(raw[0]) > 128 {
			silenceError(c, store.ErrInvalidSilence)
			return "", 0, false
		}
		after = raw[0]
	}
	return after, limit, true
}
func (s *Server) incidentSilences(c *gin.Context) { s.silenceContext(c, false) }
func (s *Server) deliverySilences(c *gin.Context) { s.silenceContext(c, true) }
func (s *Server) silenceContext(c *gin.Context, delivery bool) {
	after, limit, ok := silencePagination(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	var page store.IncidentSilencePage
	var err error
	if delivery {
		page, err = s.store.ListDeliverySilences(ctx, c.Param("deliveryID"), after, limit, time.Now().UTC())
	} else {
		page, err = s.store.ListIncidentSilences(ctx, c.Param("incidentID"), after, limit, time.Now().UTC())
	}
	if err != nil {
		silenceError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}
