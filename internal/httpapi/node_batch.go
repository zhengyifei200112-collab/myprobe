package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func decodeBatch(c *gin.Context, value any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid batch request"})
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected one JSON object"})
		return false
	}
	return true
}
func batchError(c *gin.Context, err error) {
	var conflict *store.BatchConflict
	switch {
	case errors.As(err, &conflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "conflict_node_ids": conflict.NodeIDs})
	case errors.Is(err, store.ErrBatchExpired), errors.Is(err, store.ErrBatchKey):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, store.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "node, target or owned preview not found"})
	case errors.Is(err, store.ErrBatchInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "batch operation failed; retry with the same preview and key"})
	}
}
func (s *Server) previewNodeBatch(c *gin.Context) {
	var request store.NodeBatchRequest
	if !decodeBatch(c, &request) {
		return
	}
	value, _ := c.Get("session")
	preview, err := s.store.PreviewNodeBatch(c.Request.Context(), value.(store.Session).UserID, request, time.Now().UTC())
	if err != nil {
		batchError(c, err)
		return
	}
	c.JSON(http.StatusOK, preview)
}
func (s *Server) applyNodeBatch(c *gin.Context) {
	var request struct {
		PreviewID      string `json:"preview_id"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if !decodeBatch(c, &request) {
		return
	}
	value, _ := c.Get("session")
	result, err := s.store.ApplyNodeBatch(c.Request.Context(), value.(store.Session).UserID, request.PreviewID, request.IdempotencyKey, time.Now().UTC())
	if err != nil {
		batchError(c, err)
		return
	}
	s.hub.PublishRefresh()
	c.JSON(http.StatusOK, result)
}
