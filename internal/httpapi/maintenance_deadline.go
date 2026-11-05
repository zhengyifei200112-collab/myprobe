package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Large authenticated transfers need longer than ordinary 15s reads/30s writes.
// Keep an absolute budget for upload, processing and response instead of removing
// deadlines. net/http reinstates the ordinary deadlines on the next request.
const databaseTransferTimeout = 15 * time.Minute

func beginDatabaseTransfer(c *gin.Context, upload bool) (context.CancelFunc, bool) {
	deadline := time.Now().Add(databaseTransferTimeout)
	if parent, ok := c.Request.Context().Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	controller := http.NewResponseController(c.Writer)
	set := func(err error) bool {
		// In-process ResponseRecorders have no underlying connection. Production
		// Gin writers unwrap to net/http's connection-backed response controller.
		if err == nil || errors.Is(err, http.ErrNotSupported) {
			return true
		}
		c.Header("Cache-Control", "private, no-store")
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare database transfer"})
		return false
	}
	if upload && !set(controller.SetReadDeadline(deadline)) {
		return func() {}, false
	}
	if !set(controller.SetWriteDeadline(deadline)) {
		return func() {}, false
	}
	ctx, cancel := context.WithDeadline(c.Request.Context(), deadline)
	c.Request = c.Request.WithContext(ctx)
	return cancel, true
}

type maintenanceArchiveReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r maintenanceArchiveReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
