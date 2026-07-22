package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	backuparchive "github.com/zhengyifei200112-collab/myprobe/internal/backup"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

const (
	maximumConfigImport = 10 << 20
	maximumBackupUpload = int64(2 << 30)
)

func (s *Server) exportConfiguration(c *gin.Context) {
	snapshot, err := s.store.ExportConfig(c.Request.Context(), time.Now().UTC())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to export configuration"})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="myprobe-config-%s.json"`, time.Now().UTC().Format("20060102T150405Z")))
	c.JSON(http.StatusOK, snapshot)
}

func (s *Server) importConfiguration(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maximumConfigImport)
	var request struct {
		DryRun bool                 `json:"dry_run"`
		Config store.ConfigSnapshot `json:"config"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid configuration document"})
		return
	}
	result, err := s.store.ImportConfig(c.Request.Context(), request.Config, request.DryRun)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !request.DryRun {
		s.audit(c, "import", "configuration", "", gin.H{
			"nodes_created": result.NodesCreated, "nodes_updated": result.NodesUpdated,
			"targets_created": result.TargetsAdded, "targets_updated": result.TargetsUpdated,
			"groups_created": result.GroupsAdded, "groups_updated": result.GroupsUpdated,
			"memberships_created": result.MembersAdded,
		})
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, gin.H{"result": result})
}

func (s *Server) exportDatabaseBackup(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var request struct {
		Passphrase string `json:"passphrase"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&request); err != nil || len(request.Passphrase) < 12 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "passphrase must contain at least 12 characters"})
		return
	}
	directory, err := s.databaseDirectory()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	snapshot, err := reserveTempPath(directory, ".myprobe-snapshot-*.db")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare backup"})
		return
	}
	defer os.Remove(snapshot)
	if err := s.store.ConsistentBackup(c.Request.Context(), snapshot); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create database snapshot"})
		return
	}
	plain, err := os.Open(snapshot)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read database snapshot"})
		return
	}
	defer plain.Close()
	encrypted, err := os.CreateTemp(directory, ".myprobe-backup-*.mpb")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare encrypted backup"})
		return
	}
	encryptedPath := encrypted.Name()
	defer func() { encrypted.Close(); os.Remove(encryptedPath) }()
	if err := backuparchive.Encrypt(encrypted, plain, request.Passphrase); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt backup"})
		return
	}
	if err := encrypted.Sync(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to finalize backup"})
		return
	}
	info, err := encrypted.Stat()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to finalize backup"})
		return
	}
	if _, err := encrypted.Seek(0, io.SeekStart); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read encrypted backup"})
		return
	}
	s.audit(c, "export", "database_backup", "", gin.H{"encrypted": true})
	c.Header("Cache-Control", "private, no-store")
	name := fmt.Sprintf("myprobe-backup-%s.mpb", time.Now().UTC().Format("20060102T150405Z"))
	c.DataFromReader(http.StatusOK, info.Size(), "application/octet-stream", encrypted, map[string]string{"Content-Disposition": fmt.Sprintf(`attachment; filename="%s"`, name)})
}

func (s *Server) stageDatabaseRestore(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maximumBackupUpload)
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or oversized restore upload"})
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	passphrase := c.Request.FormValue("passphrase")
	if len(passphrase) < 12 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "passphrase must contain at least 12 characters"})
		return
	}
	upload, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "backup file is required"})
		return
	}
	defer upload.Close()
	if header.Size <= 0 || header.Size > maximumBackupUpload {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or oversized backup file"})
		return
	}
	directory, err := s.databaseDirectory()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	decrypted, err := os.CreateTemp(directory, ".myprobe-restore-*.db")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare restore"})
		return
	}
	decryptedPath := decrypted.Name()
	staged := false
	defer func() {
		decrypted.Close()
		if !staged {
			os.Remove(decryptedPath)
		}
	}()
	if err := backuparchive.Decrypt(decrypted, upload, passphrase); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := decrypted.Sync(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to finalize restore"})
		return
	}
	if err := decrypted.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to finalize restore"})
		return
	}
	pending, err := store.StageDatabaseRestore(c.Request.Context(), s.store.DatabasePath(), decryptedPath)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrRestorePending) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	staged = true
	s.audit(c, "stage_restore", "database_backup", "", gin.H{"restart_required": true})
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusAccepted, gin.H{"staged": true, "restart_required": true, "pending_file": filepath.Base(pending)})
}

func (s *Server) databaseDirectory() (string, error) {
	path := s.store.DatabasePath()
	if path == "" || path == ":memory:" {
		return "", errors.New("database maintenance requires a file-backed SQLite database")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Dir(absolute), nil
}

func reserveTempPath(directory, pattern string) (string, error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}
