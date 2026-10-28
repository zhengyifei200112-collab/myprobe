package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	_ "time/tzdata"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

var dailyTimePattern = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)
var ErrBusy = RepositoryError("backup_worker_busy")

type Scheduler struct {
	store            *store.Store
	passphraseFile   string
	verifyChannels   func(context.Context, *store.Store) error
	validateChannels func(context.Context, []string) error
	workerRoot       *os.Root
	workerLock       *os.File
	operation        sync.Mutex
	running          atomic.Bool
	lastError        atomic.Value
	runtimeCtx       context.Context
	active           atomic.Value
}
type Runtime struct {
	Running   bool   `json:"running"`
	Available bool   `json:"available"`
	ErrorCode string `json:"error_code,omitempty"`
	Operation string `json:"operation,omitempty"`
}
type Status struct {
	Config          store.BackupSchedule       `json:"config"`
	Runtime         Runtime                    `json:"runtime"`
	PassphraseReady bool                       `json:"passphrase_ready"`
	Runs            []store.BackupRun          `json:"runs"`
	Notifications   []store.SystemNotification `json:"notifications"`
}

func NewScheduler(ctx context.Context, s *store.Store, passphraseFile string, verify func(context.Context, *store.Store) error, validate func(context.Context, []string) error) (*Scheduler, error) {
	if s.DatabasePath() == ":memory:" {
		return nil, RepositoryError("backup_requires_file_database")
	}
	absolute, err := sourcePath(s.DatabasePath())
	if err != nil {
		return nil, RepositoryError("invalid_database_path")
	}
	root, err := os.OpenRoot(filepath.Dir(absolute))
	if err != nil {
		return nil, RepositoryError("backup_worker_unavailable")
	}
	hash := sha256.Sum256([]byte(filepath.Clean(absolute)))
	name := ".myprobe-backup-worker-" + hex.EncodeToString(hash[:16]) + ".lock"
	info, err := root.Lstat(name)
	if err == nil && !info.Mode().IsRegular() {
		root.Close()
		return nil, RepositoryError("backup_worker_unavailable")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		root.Close()
		return nil, RepositoryError("backup_worker_unavailable")
	}
	file, err := root.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		root.Close()
		return nil, RepositoryError("backup_worker_unavailable")
	}
	current, statErr := root.Lstat(name)
	opened, openedErr := file.Stat()
	if statErr != nil || openedErr != nil || !current.Mode().IsRegular() || !os.SameFile(current, opened) {
		file.Close()
		root.Close()
		return nil, RepositoryError("backup_worker_unavailable")
	}
	if err = lockRepository(file); err != nil {
		file.Close()
		root.Close()
		return nil, RepositoryError("backup_worker_in_use")
	}
	scheduler := &Scheduler{store: s, passphraseFile: passphraseFile, verifyChannels: verify, validateChannels: validate, workerRoot: root, workerLock: file, runtimeCtx: ctx}
	scheduler.lastError.Store("")
	scheduler.active.Store("")
	if err = s.InterruptBackupRuns(ctx, time.Now().UTC()); err != nil {
		scheduler.Close()
		return nil, RepositoryError("backup_reconcile_failed")
	}
	return scheduler, nil
}
func (s *Scheduler) Close() error {
	s.operation.Lock()
	defer s.operation.Unlock()
	err := s.workerLock.Close()
	rootErr := s.workerRoot.Close()
	if err != nil {
		return err
	}
	return rootErr
}
func (s *Scheduler) Diagnostics() Runtime {
	code, _ := s.lastError.Load().(string)
	active, _ := s.active.Load().(string)
	return Runtime{Running: s.running.Load(), Available: true, ErrorCode: code, Operation: active}
}
func (s *Scheduler) passphrase() (string, error) {
	if s.passphraseFile == "" {
		return "", RepositoryError("backup_passphrase_unavailable")
	}
	info, err := os.Lstat(s.passphraseFile)
	if err != nil || !info.Mode().IsRegular() {
		return "", RepositoryError("backup_passphrase_unavailable")
	}
	file, err := os.Open(s.passphraseFile)
	if err != nil {
		return "", RepositoryError("backup_passphrase_unavailable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", RepositoryError("backup_passphrase_unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(raw) > 4096 {
		return "", RepositoryError("backup_passphrase_unavailable")
	}
	pass := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if len(pass) < minimumPassphrase {
		return "", RepositoryError("backup_passphrase_unavailable")
	}
	return pass, nil
}
func NormalizeSchedule(c store.BackupSchedule) (store.BackupSchedule, error) {
	if !dailyTimePattern.MatchString(c.DailyTime) || len(c.Timezone) > 100 || c.Timezone == "" || c.Keep < 1 || c.Keep > 1000 || len(c.FailureChannelIDs) > 20 {
		return c, RepositoryError("invalid_backup_schedule")
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil || c.Timezone == "Local" {
		return c, RepositoryError("invalid_backup_timezone")
	}
	if c.Directory != "" {
		if !filepath.IsAbs(c.Directory) || len(c.Directory) > 4096 {
			return c, RepositoryError("invalid_backup_directory")
		}
		c.Directory = filepath.Clean(c.Directory)
	}
	if c.Enabled && (c.Directory == "" || len(c.FailureChannelIDs) == 0) {
		return c, RepositoryError("backup_directory_and_failure_channel_required")
	}
	seen := map[string]bool{}
	for _, id := range c.FailureChannelIDs {
		if id == "" || len(id) > 128 || seen[id] {
			return c, RepositoryError("invalid_backup_failure_channels")
		}
		seen[id] = true
	}
	if c.FailureChannelIDs == nil {
		c.FailureChannelIDs = []string{}
	}
	return c, nil
}
func (s *Scheduler) Save(ctx context.Context, c store.BackupSchedule) error {
	if !s.operation.TryLock() {
		return ErrBusy
	}
	defer s.operation.Unlock()
	c, err := NormalizeSchedule(c)
	if err != nil {
		return err
	}
	if c.Enabled {
		if _, err = s.passphrase(); err != nil {
			return err
		}
		if s.validateChannels == nil {
			return RepositoryError("backup_failure_channel_unavailable")
		}
		if err = s.validateChannels(ctx, c.FailureChannelIDs); err != nil {
			return RepositoryError("backup_failure_channel_unavailable")
		}
		repo, err := OpenRepository(c.Directory, s.store.DatabasePath(), c.Keep)
		if err != nil {
			return err
		}
		defer repo.Close()
		if err = repo.Validate(ctx, s.store); err != nil {
			return err
		}
	}
	if err = s.store.SaveBackupSchedule(ctx, c); err != nil {
		return operationError(ctx, "backup_settings_save_failed")
	}
	return nil
}
func (s *Scheduler) Status(ctx context.Context) (Status, error) {
	c, err := s.store.BackupSchedule(ctx)
	if err != nil {
		return Status{}, RepositoryError("backup_status_unavailable")
	}
	runs, err := s.store.BackupRuns(ctx)
	if err != nil {
		return Status{}, RepositoryError("backup_status_unavailable")
	}
	notifications, err := s.store.SystemNotifications(ctx)
	if err != nil {
		return Status{}, RepositoryError("backup_status_unavailable")
	}
	_, passErr := s.passphrase()
	return Status{Config: c, Runtime: s.Diagnostics(), PassphraseReady: passErr == nil, Runs: runs, Notifications: notifications}, nil
}
func (s *Scheduler) Run(ctx context.Context) {
	s.running.Store(true)
	defer s.running.Store(false)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		_, err := s.Tick(ctx, time.Now().UTC(), false)
		if err != nil && ctx.Err() == nil && !errors.Is(err, ErrBusy) {
			s.lastError.Store(safeCode(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func safeCode(err error) string {
	var code RepositoryError
	if errors.As(err, &code) {
		return string(code)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "backup_process_interrupted"
	}
	return "backup_operation_failed"
}

// A missed time is caught up once for the current local date. Older missed
// dates are not replayed. A nonexistent DST time runs at the next observed
// local minute; a repeated time still has only one durable date claim.
func (s *Scheduler) Tick(ctx context.Context, now time.Time, manual bool) (*store.BackupRun, error) {
	if !s.operation.TryLock() {
		return nil, ErrBusy
	}
	defer s.operation.Unlock()
	return s.tickLocked(ctx, now, manual)
}

func (s *Scheduler) tickLocked(ctx context.Context, now time.Time, manual bool) (*store.BackupRun, error) {
	s.active.Store("backup")
	defer s.active.Store("")
	c, err := s.store.BackupSchedule(ctx)
	if err != nil {
		return nil, RepositoryError("backup_settings_unavailable")
	}
	if !c.Enabled {
		return nil, nil
	}
	c, err = NormalizeSchedule(c)
	if err != nil {
		return nil, err
	}
	date := ""
	if !manual {
		location, _ := time.LoadLocation(c.Timezone)
		local := now.In(location)
		if local.Format("15:04") < c.DailyTime {
			return nil, nil
		}
		date = local.Format("2006-01-02")
	}
	// Claim before preflight, so a broken directory/key is a recorded failure
	// rather than silent retry on every wake. Manual retry remains explicit.
	run, err := s.store.BeginBackupRun(ctx, date, c.FailureChannelIDs, now)
	if err != nil {
		return nil, RepositoryError("backup_claim_failed")
	}
	if run == nil {
		return nil, nil
	}
	workCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	var artifact Artifact
	operationErr := func() error {
		pass, err := s.passphrase()
		if err != nil {
			return err
		}
		repo, err := OpenRepository(c.Directory, s.store.DatabasePath(), c.Keep)
		if err != nil {
			return err
		}
		defer repo.Close()
		artifact, err = repo.Generate(workCtx, s.store, pass, now)
		if err != nil {
			return err
		}
		_, err = repo.Prune(workCtx, artifact.ID)
		return err
	}()
	status, code := "completed", ""
	if operationErr != nil {
		status = "failed"
		code = safeCode(operationErr)
		if workCtx.Err() != nil {
			status = "interrupted"
			code = "backup_process_interrupted"
		}
	}
	var encoded json.RawMessage
	if artifact.ID != "" {
		encoded, _ = json.Marshal(artifact)
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	if err = s.store.FinishBackupRun(finishCtx, run.ID, status, code, encoded, time.Now().UTC()); err != nil {
		s.lastError.Store("backup_result_persist_failed")
		return run, RepositoryError("backup_result_persist_failed")
	}
	s.lastError.Store(code)
	run.Status = status
	run.ErrorCode = code
	run.Artifact = encoded
	return run, operationErr
}

func (s *Scheduler) VerifyRecovery(ctx context.Context, runID string) (Artifact, error) {
	if !s.operation.TryLock() {
		return Artifact{}, ErrBusy
	}
	defer s.operation.Unlock()
	return s.verifyRecoveryLocked(ctx, runID)
}
func (s *Scheduler) verifyRecoveryLocked(ctx context.Context, runID string) (Artifact, error) {
	s.active.Store("recovery")
	defer s.active.Store("")
	c, err := s.store.BackupSchedule(ctx)
	if err != nil {
		return Artifact{}, RepositoryError("backup_settings_unavailable")
	}
	raw, err := s.store.BackupRunArtifact(ctx, runID)
	if err != nil {
		return Artifact{}, RepositoryError("recovery_artifact_unavailable")
	}
	var original Artifact
	if json.Unmarshal(raw, &original) != nil || original.ID == "" {
		return Artifact{}, RepositoryError("recovery_artifact_unavailable")
	}
	pass, err := s.passphrase()
	if err != nil {
		return Artifact{}, err
	}
	repo, err := OpenRepository(c.Directory, s.store.DatabasePath(), c.Keep)
	if err != nil {
		return Artifact{}, err
	}
	defer repo.Close()
	if err = repo.Validate(ctx, s.store); err != nil {
		return Artifact{}, err
	}
	artifact, err := repo.VerifyRecovery(ctx, original.FileName, pass, s.verifyChannels)
	if err != nil {
		return Artifact{}, err
	}
	encoded, _ := json.Marshal(artifact)
	if err = s.store.RecordBackupRecovery(ctx, runID, encoded); err != nil {
		return Artifact{}, RepositoryError("recovery_record_failed")
	}
	return artifact, nil
}

func (s *Scheduler) RequestManual(ctx context.Context) (bool, error) {
	if !s.operation.TryLock() {
		return false, ErrBusy
	}
	c, err := s.store.BackupSchedule(ctx)
	if err != nil {
		s.operation.Unlock()
		return false, RepositoryError("backup_settings_unavailable")
	}
	if !c.Enabled {
		s.operation.Unlock()
		return false, nil
	}
	if s.runtimeCtx.Err() != nil {
		s.operation.Unlock()
		return false, RepositoryError("backup_worker_unavailable")
	}
	s.active.Store("backup")
	go func() {
		defer s.operation.Unlock()
		_, err := s.tickLocked(s.runtimeCtx, time.Now().UTC(), true)
		if err != nil {
			s.lastError.Store(safeCode(err))
		}
	}()
	return true, nil
}
func (s *Scheduler) RequestRecovery(ctx context.Context, runID string) (bool, error) {
	if !s.operation.TryLock() {
		return false, ErrBusy
	}
	raw, err := s.store.BackupRunArtifact(ctx, runID)
	var artifact Artifact
	if err != nil || json.Unmarshal(raw, &artifact) != nil || artifact.ID == "" {
		s.operation.Unlock()
		return false, nil
	}
	if s.runtimeCtx.Err() != nil {
		s.operation.Unlock()
		return false, RepositoryError("backup_worker_unavailable")
	}
	s.active.Store("recovery")
	go func() {
		defer s.operation.Unlock()
		workCtx, cancel := context.WithTimeout(s.runtimeCtx, 30*time.Minute)
		defer cancel()
		_, err := s.verifyRecoveryLocked(workCtx, runID)
		code := ""
		if err != nil {
			code = safeCode(err)
		}
		s.lastError.Store(code)
	}()
	return true, nil
}
