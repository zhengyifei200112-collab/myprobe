package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/backup"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func WithBackupScheduler(worker *backup.Scheduler) Option {
	return func(s *Server) { s.scheduledBackups = worker }
}
func (s *Server) backupWorker(c *gin.Context) *backup.Scheduler {
	if s.scheduledBackups == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "backup_worker_unavailable"})
		return nil
	}
	return s.scheduledBackups
}
func (s *Server) scheduledBackupStatus(c *gin.Context) {
	worker := s.backupWorker(c)
	if worker == nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	status, err := worker.Status(ctx)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "backup_status_unavailable"})
		return
	}
	c.JSON(http.StatusOK, status)
}
func (s *Server) saveBackupSchedule(c *gin.Context) {
	worker := s.backupWorker(c)
	if worker == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var config store.BackupSchedule
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_backup_schedule"})
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_backup_schedule"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if err := worker.Save(ctx, config); err != nil {
		s.backupError(c, err)
		return
	}
	s.audit(c, "update", "backup_schedule", "", gin.H{"enabled": config.Enabled, "daily_time": config.DailyTime, "timezone": config.Timezone, "keep": config.Keep, "failure_channel_count": len(config.FailureChannelIDs)})
	c.JSON(http.StatusOK, gin.H{"saved": true})
}
func (s *Server) runScheduledBackup(c *gin.Context) {
	worker := s.backupWorker(c)
	if worker == nil {
		return
	}
	// Manual execution is asynchronous: long snapshots do not depend on the
	// browser request or HTTP write timeout. The task remains visible after reload.
	accepted, err := worker.RequestManual(c.Request.Context())
	if err != nil {
		s.backupError(c, err)
		return
	}
	if !accepted {
		c.JSON(http.StatusConflict, gin.H{"error": "backup_schedule_disabled"})
		return
	}
	s.audit(c, "create", "scheduled_backup", "", nil)
	c.JSON(http.StatusAccepted, gin.H{"accepted": true})
}
func (s *Server) verifyScheduledBackup(c *gin.Context) {
	worker := s.backupWorker(c)
	if worker == nil {
		return
	}
	accepted, err := worker.RequestRecovery(c.Request.Context(), c.Param("id"))
	if err != nil {
		s.backupError(c, err)
		return
	}
	if !accepted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "recovery_artifact_unavailable"})
		return
	}
	s.audit(c, "verify", "backup_recovery", c.Param("id"), nil)
	c.JSON(http.StatusAccepted, gin.H{"accepted": true})
}
func (s *Server) backupError(c *gin.Context, err error) {
	code := "backup_operation_failed"
	var known backup.RepositoryError
	if errors.As(err, &known) {
		code = string(known)
	}
	status := http.StatusBadRequest
	if errors.Is(err, backup.ErrBusy) {
		status = http.StatusConflict
	}
	c.JSON(status, gin.H{"error": code})
}
