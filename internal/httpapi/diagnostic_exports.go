package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/diagnostics"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func decodeDiagnosticRequest(c *gin.Context, value any, allowEmpty bool) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(value)
	if allowEmpty && err == io.EOF {
		return true
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_diagnostic_request"})
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_diagnostic_request"})
		return false
	}
	return true
}
func (s *Server) prepareDiagnosticExport(c *gin.Context) {
	var request struct{}
	if !decodeDiagnosticRequest(c, &request, true) {
		return
	}
	session := c.MustGet("session").(store.Session)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	bundle := s.collectDiagnosticBundle(ctx)
	if ctx.Err() != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "diagnostic_collection_unavailable"})
		return
	}
	body, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "diagnostic_collection_unavailable"})
		return
	}
	body = append(body, '\n')
	metadata, err := s.diagnosticExports.Prepare(session.ID, body, time.Now())
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, diagnostics.ErrExportPreviewCapacity) {
			status = http.StatusTooManyRequests
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"preview": metadata, "bundle": json.RawMessage(body)})
}

var diagnosticPreviewID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (s *Server) downloadDiagnosticExport(c *gin.Context) {
	var request struct {
		ID string `json:"preview_id"`
	}
	if !decodeDiagnosticRequest(c, &request, false) {
		return
	}
	if !diagnosticPreviewID.MatchString(request.ID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_diagnostic_request"})
		return
	}
	session := c.MustGet("session").(store.Session)
	metadata, body, err := s.diagnosticExports.Read(session.ID, request.ID, time.Now())
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "diagnostic_preview_missing"})
		return
	}
	// Audit is a required gate, not a best-effort side effect. It records preparation
	// of the response, not proof that the browser saved or shared the file.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err = s.store.LogAudit(ctx, session.UserID, "export", "diagnostic_bundle", metadata.ID, c.ClientIP(), gin.H{"format": "myprobe.diagnostics.v1", "bytes": metadata.Bytes, "sha256": metadata.SHA256, "result": "response_prepared"}); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "diagnostic_audit_unavailable"})
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+metadata.FileName+`"`)
	c.Header("X-Content-SHA256", metadata.SHA256)
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}
