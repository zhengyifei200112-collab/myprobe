package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func (s *Server) reorderNodes(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
	defer c.Request.Body.Close()
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var request store.NodeOrderRequest
	if err := decoder.Decode(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid node order request"})
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unexpected trailing data"})
		return
	}
	session := c.MustGet("session").(store.Session)
	changed, err := s.store.ReorderNodes(c.Request.Context(), session.UserID, request)
	switch {
	case errors.Is(err, store.ErrNodeOrderInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": "provide a complete unique order of 1 to 1000 nodes"})
	case errors.Is(err, store.ErrNodeOrderConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "节点或排序已变化，请重新载入后调整。"})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save node order"})
	default:
		c.JSON(http.StatusOK, gin.H{"changed": changed})
	}
}
