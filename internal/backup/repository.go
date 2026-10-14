package backup

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

const ownerFile = ".myprobe-backup-owner-v1.json"

var archiveName = regexp.MustCompile(`^myprobe-backup-[0-9]{8}T[0-9]{6}Z-[a-f0-9]{32}\.mpb$`)

// Artifact is safe metadata, not a recovery claim. Passphrases and directory
// paths are never serialized with it.
type Artifact struct {
	Version            int                           `json:"version"`
	OwnerID            string                        `json:"owner_id"`
	ID                 string                        `json:"id"`
	FileName           string                        `json:"file_name"`
	StartedAt          time.Time                     `json:"started_at"`
	CompletedAt        time.Time                     `json:"completed_at"`
	Bytes              int64                         `json:"bytes"`
	SHA256             string                        `json:"sha256"`
	ArchiveVerified    bool                          `json:"archive_verified"`
	RecoveryVerified   bool                          `json:"recovery_verified"`
	RecoveryVerifiedAt *time.Time                    `json:"recovery_verified_at,omitempty"`
	RecoveryEvidence   *store.BackupRecoveryEvidence `json:"recovery_evidence,omitempty"`
}

type signedArtifact struct {
	Artifact  Artifact `json:"artifact"`
	Signature string   `json:"signature"`
}
type ownership struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Key     string `json:"key"`
	Source  string `json:"source"`
}

// Repository anchors all managed archive operations to one directory handle.
// The directory is operator-controlled and must not be shared with untrusted
// writers. A signing key authenticates ownership, never archive decryption.
type Repository struct {
	mu               sync.Mutex
	root             *os.Root
	lock             *os.File
	owner            ownership
	key              []byte
	keep             int
	scratchDirectory string
	encrypt          func(io.Writer, io.Reader, string) error
	publish          func(string, string) error
	freeSpace        func(context.Context, string) (uint64, error)
	now              func() time.Time
}

type RepositoryError string

func (e RepositoryError) Error() string { return string(e) }

func randomHex(n int) (string, error) {
	value := make([]byte, n)
	_, err := rand.Read(value)
	return hex.EncodeToString(value), err
}

func OpenRepository(directory, databasePath string, keep int) (*Repository, error) {
	if directory == "" || databasePath == "" || databasePath == ":memory:" || keep < 1 || keep > 1000 {
		return nil, RepositoryError("invalid_backup_repository")
	}
	abs, err := sourcePath(databasePath)
	if err != nil {
		return nil, RepositoryError("invalid_database_path")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, RepositoryError("backup_directory_unavailable")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, RepositoryError("backup_directory_unavailable")
	}
	var lock *os.File
	fail := func(code string) (*Repository, error) {
		if lock != nil {
			lock.Close()
		}
		root.Close()
		return nil, RepositoryError(code)
	}
	lockInfo, lockErr := root.Lstat(".myprobe-backup.lock")
	if lockErr == nil && !lockInfo.Mode().IsRegular() {
		return fail("backup_lock_unavailable")
	}
	if lockErr != nil && !errors.Is(lockErr, os.ErrNotExist) {
		return fail("backup_lock_unavailable")
	}
	lock, err = root.OpenFile(".myprobe-backup.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fail("backup_lock_unavailable")
	}
	currentLock, lockStatErr := root.Lstat(".myprobe-backup.lock")
	openedLock, openedLockErr := lock.Stat()
	if lockStatErr != nil || openedLockErr != nil || !currentLock.Mode().IsRegular() || !os.SameFile(currentLock, openedLock) {
		return fail("backup_lock_unavailable")
	}
	if err := lockRepository(lock); err != nil {
		return fail("backup_repository_in_use")
	}
	source := sha256.Sum256([]byte(filepath.Clean(abs)))
	r := &Repository{root: root, lock: lock, keep: keep, scratchDirectory: filepath.Dir(abs), encrypt: Encrypt, publish: root.Link, now: time.Now, freeSpace: func(ctx context.Context, path string) (uint64, error) {
		usage, err := disk.UsageWithContext(ctx, path)
		if err != nil {
			return 0, err
		}
		return usage.Free, nil
	}}
	info, err := root.Lstat(ownerFile)
	if errors.Is(err, os.ErrNotExist) {
		id, e := randomHex(16)
		if e != nil {
			return fail("backup_identity_failed")
		}
		key, e := randomHex(32)
		if e != nil {
			return fail("backup_identity_failed")
		}
		r.owner = ownership{Version: 1, ID: id, Key: key, Source: hex.EncodeToString(source[:])}
		data, e := json.Marshal(r.owner)
		if e != nil {
			return fail("backup_identity_failed")
		}
		ownerPart := ".myprobe-backup-owner-part-" + id
		file, e := root.OpenFile(ownerPart, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if e != nil {
			return fail("backup_identity_unavailable")
		}
		_, e = file.Write(data)
		if e == nil {
			e = file.Sync()
		}
		closeErr := file.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			root.Remove(ownerPart)
			return fail("backup_identity_failed")
		}
		if err := root.Link(ownerPart, ownerFile); err != nil {
			root.Remove(ownerPart)
			return fail("backup_identity_failed")
		}
		root.Remove(ownerPart)
	} else {
		if err != nil || !info.Mode().IsRegular() {
			return fail("backup_identity_unavailable")
		}
		file, e := root.Open(ownerFile)
		if e != nil {
			return fail("backup_identity_unavailable")
		}
		opened, e := file.Stat()
		if e != nil || !os.SameFile(info, opened) {
			file.Close()
			return fail("backup_identity_unavailable")
		}
		data, e := io.ReadAll(io.LimitReader(file, 4097))
		file.Close()
		if e != nil || len(data) > 4096 || json.Unmarshal(data, &r.owner) != nil {
			return fail("backup_identity_invalid")
		}
	}
	ownerID, idErr := hex.DecodeString(r.owner.ID)
	if r.owner.Version != 1 || idErr != nil || len(ownerID) != 16 || r.owner.Source != hex.EncodeToString(source[:]) {
		return fail("backup_directory_owner_mismatch")
	}
	r.key, err = hex.DecodeString(r.owner.Key)
	if err != nil || len(r.key) != 32 {
		return fail("backup_identity_invalid")
	}
	if err := r.recoverScratch(); err != nil {
		return fail("backup_scratch_cleanup_failed")
	}
	return r, nil
}

// VerifyRecovery opens an isolated restored database through the application
// Store and proves its encrypted channels can be read with the separately held
// application key. This is a bounded local exercise, not a production failover.
func (r *Repository) VerifyRecovery(ctx context.Context, name, passphrase string, verifyChannels func(context.Context, *store.Store) error) (Artifact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if verifyChannels == nil {
		return Artifact{}, RepositoryError("recovery_application_key_unavailable")
	}
	a, err := r.artifact(ctx, name)
	if err != nil {
		return Artifact{}, operationError(ctx, "recovery_artifact_unavailable")
	}
	if uint64(a.Bytes) > (^uint64(0)-64*(1<<20))/2 {
		return Artifact{}, RepositoryError("backup_size_unavailable")
	}
	free, spaceErr := r.freeSpace(ctx, r.scratchDirectory)
	if spaceErr != nil {
		return Artifact{}, operationError(ctx, "backup_space_unavailable")
	}
	if free < 2*uint64(a.Bytes)+64*(1<<20) {
		return Artifact{}, RepositoryError("backup_insufficient_space")
	}
	work, record, err := r.newScratch("recovery")
	if err != nil {
		return Artifact{}, RepositoryError("recovery_scratch_unavailable")
	}
	defer func() { _ = r.cleanupScratch(record) }()
	source, err := r.root.Open(name)
	if err != nil {
		return Artifact{}, RepositoryError("recovery_artifact_unavailable")
	}
	defer source.Close()
	restoredPath := filepath.Join(work, "recovered.db")
	restored, err := os.OpenFile(restoredPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Artifact{}, RepositoryError("recovery_scratch_unavailable")
	}
	err = Decrypt(contextWriter{ctx, restored}, contextReader{ctx, source}, passphrase)
	if err == nil {
		err = restored.Sync()
	}
	closeErr := restored.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return Artifact{}, operationError(ctx, "recovery_decryption_failed")
	}
	if err = store.ValidateDatabaseFile(ctx, restoredPath); err != nil {
		return Artifact{}, operationError(ctx, "recovery_database_invalid")
	}
	recovered, err := store.Open(ctx, restoredPath)
	if err != nil {
		return Artifact{}, operationError(ctx, "recovery_database_invalid")
	}
	evidence, err := recovered.ReadRecoveredState(ctx)
	if err == nil {
		if e := verifyChannels(ctx, recovered); e != nil {
			err = RepositoryError("recovery_application_key_unavailable")
		}
	}
	closeErr = recovered.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return Artifact{}, operationError(ctx, safeCode(err))
	}
	if err = ctx.Err(); err != nil {
		return Artifact{}, err
	}
	now := r.now().UTC()
	a.RecoveryVerified = true
	a.RecoveryVerifiedAt = &now
	a.RecoveryEvidence = &evidence
	canonical, _ := json.Marshal(a)
	mac := hmac.New(sha256.New, r.key)
	mac.Write(canonical)
	raw, _ := json.Marshal(signedArtifact{a, hex.EncodeToString(mac.Sum(nil))})
	id, err := randomHex(16)
	if err != nil {
		return Artifact{}, RepositoryError("recovery_record_failed")
	}
	part := ".myprobe-recovery-record-" + id
	file, err := r.root.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Artifact{}, RepositoryError("recovery_record_failed")
	}
	defer func() { file.Close(); _ = r.root.Remove(part) }()
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr = file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return Artifact{}, RepositoryError("recovery_record_failed")
	}
	if err = r.root.Rename(part, name+".json"); err != nil {
		return Artifact{}, RepositoryError("recovery_record_failed")
	}
	return a, nil
}

func (r *Repository) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	lockErr := r.lock.Close()
	rootErr := r.root.Close()
	if lockErr != nil {
		return lockErr
	}
	return rootErr
}

func operationError(ctx context.Context, code string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return RepositoryError(code)
}

// Validate checks actual creation/sync/no-clobber publication and conservative
// scratch/output space. It is also the configuration-save preflight provider.
func (r *Repository) Validate(ctx context.Context, source *store.Store) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.validate(ctx, source)
}

func (r *Repository) validate(ctx context.Context, source *store.Store) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	evidence := source.DatabaseDiagnostics(ctx)
	if evidence.Status != "ok" || evidence.AllocatedBytes == nil || *evidence.AllocatedBytes < 0 {
		return operationError(ctx, "backup_size_unavailable")
	}
	// Two plaintext verification/snapshot files and one encrypted output.
	// Reserve the combined budget on both volumes, including the same-volume case.
	if uint64(*evidence.AllocatedBytes) > (^uint64(0)-64*(1<<20))/3 {
		return RepositoryError("backup_size_unavailable")
	}
	required := 3*uint64(*evidence.AllocatedBytes) + 64*(1<<20)
	for _, path := range []string{r.root.Name(), filepath.Dir(source.DatabasePath())} {
		free, err := r.freeSpace(ctx, path)
		if err != nil {
			return operationError(ctx, "backup_space_unavailable")
		}
		if free < required {
			return RepositoryError("backup_insufficient_space")
		}
	}
	id, err := randomHex(16)
	if err != nil {
		return RepositoryError("backup_preflight_failed")
	}
	name := ".myprobe-backup-probe-" + id
	file, err := r.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return RepositoryError("backup_directory_unwritable")
	}
	defer func() { file.Close(); r.root.Remove(name); r.root.Remove(name + ".link") }()
	if _, err := file.Write([]byte("probe")); err != nil {
		return RepositoryError("backup_directory_unwritable")
	}
	if err := file.Sync(); err != nil {
		return RepositoryError("backup_sync_failed")
	}
	if err := r.root.Link(name, name+".link"); err != nil {
		return RepositoryError("backup_atomic_publish_unavailable")
	}
	return ctx.Err()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (c contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.reader.Read(p)
}

type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (c contextWriter) Write(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.writer.Write(p)
}

// Generate returns only after authenticated decryption matches the consistent
// snapshot and both ciphertext and signed completion metadata are synced.
// A successful archive check is not an application recovery exercise.
func (r *Repository) Generate(ctx context.Context, source *store.Store, passphrase string, now time.Time) (Artifact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Artifact{}, err
	}
	if len(passphrase) < minimumPassphrase {
		return Artifact{}, RepositoryError("backup_passphrase_unavailable")
	}
	path := source.DatabasePath()
	abs, err := sourcePath(path)
	if err != nil {
		return Artifact{}, RepositoryError("invalid_database_path")
	}
	identity := sha256.Sum256([]byte(filepath.Clean(abs)))
	if r.owner.Source != hex.EncodeToString(identity[:]) {
		return Artifact{}, RepositoryError("backup_source_mismatch")
	}
	if err := r.validate(ctx, source); err != nil {
		return Artifact{}, err
	}
	work, record, err := r.newScratch("backup")
	if err != nil {
		return Artifact{}, RepositoryError("backup_scratch_unavailable")
	}
	defer func() { _ = r.cleanupScratch(record) }()
	snapshot := filepath.Join(work, "snapshot.db")
	if err := source.ConsistentBackup(ctx, snapshot); err != nil {
		return Artifact{}, operationError(ctx, "backup_snapshot_failed")
	}
	plain, err := os.Open(snapshot)
	if err != nil {
		return Artifact{}, RepositoryError("backup_snapshot_unavailable")
	}
	defer plain.Close()
	id, err := randomHex(16)
	if err != nil {
		return Artifact{}, RepositoryError("backup_identity_failed")
	}
	part := ".myprobe-backup-part-" + id
	file, err := r.root.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return Artifact{}, RepositoryError("backup_output_unavailable")
	}
	defer func() { file.Close(); _ = r.root.Remove(part) }()
	hash := sha256.New()
	if err := r.encrypt(contextWriter{ctx, io.MultiWriter(file, hash)}, contextReader{ctx, plain}, passphrase); err != nil {
		return Artifact{}, operationError(ctx, "backup_encryption_failed")
	}
	if err := file.Sync(); err != nil {
		return Artifact{}, RepositoryError("backup_sync_failed")
	}
	info, err := file.Stat()
	if err != nil {
		return Artifact{}, RepositoryError("backup_output_unavailable")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Artifact{}, RepositoryError("backup_validation_failed")
	}
	verifiedPath := filepath.Join(work, "verified.db")
	verified, err := os.OpenFile(verifiedPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return Artifact{}, RepositoryError("backup_validation_failed")
	}
	defer verified.Close()
	if err := Decrypt(contextWriter{ctx, verified}, contextReader{ctx, file}, passphrase); err != nil {
		return Artifact{}, operationError(ctx, "backup_validation_failed")
	}
	if err := verified.Sync(); err != nil {
		return Artifact{}, RepositoryError("backup_validation_failed")
	}
	if _, err := plain.Seek(0, io.SeekStart); err != nil {
		return Artifact{}, RepositoryError("backup_validation_failed")
	}
	if _, err := verified.Seek(0, io.SeekStart); err != nil {
		return Artifact{}, RepositoryError("backup_validation_failed")
	}
	originalHash, restoredHash := sha256.New(), sha256.New()
	if _, err := io.Copy(originalHash, contextReader{ctx, plain}); err != nil {
		return Artifact{}, operationError(ctx, "backup_validation_failed")
	}
	if _, err := io.Copy(restoredHash, contextReader{ctx, verified}); err != nil {
		return Artifact{}, operationError(ctx, "backup_validation_failed")
	}
	if !hmac.Equal(originalHash.Sum(nil), restoredHash.Sum(nil)) || store.ValidateDatabaseFile(ctx, verifiedPath) != nil {
		return Artifact{}, operationError(ctx, "backup_validation_failed")
	}
	if err := ctx.Err(); err != nil {
		return Artifact{}, err
	}
	name := "myprobe-backup-" + now.UTC().Format("20060102T150405Z") + "-" + id + ".mpb"
	artifact := Artifact{Version: 1, OwnerID: r.owner.ID, ID: id, FileName: name, StartedAt: now.UTC(), CompletedAt: r.now().UTC(), Bytes: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil)), ArchiveVerified: true}
	if err := file.Close(); err != nil {
		return Artifact{}, RepositoryError("backup_sync_failed")
	}
	if err := r.publish(part, name); err != nil {
		return Artifact{}, RepositoryError("backup_publish_failed")
	}
	data, err := json.Marshal(artifact)
	if err != nil {
		return Artifact{}, RepositoryError("backup_manifest_failed")
	}
	mac := hmac.New(sha256.New, r.key)
	mac.Write(data)
	envelope, err := json.Marshal(signedArtifact{artifact, hex.EncodeToString(mac.Sum(nil))})
	if err != nil {
		return Artifact{}, RepositoryError("backup_manifest_failed")
	}
	metadata := part + ".json"
	manifest, err := r.root.OpenFile(metadata, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Artifact{}, RepositoryError("backup_manifest_failed")
	}
	defer func() { manifest.Close(); _ = r.root.Remove(metadata) }()
	_, err = manifest.Write(envelope)
	if err == nil {
		err = manifest.Sync()
	}
	closeErr := manifest.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return Artifact{}, RepositoryError("backup_manifest_failed")
	}
	if err := r.publish(metadata, name+".json"); err != nil {
		return Artifact{}, RepositoryError("backup_manifest_failed")
	}
	return artifact, nil
}

func (r *Repository) artifact(ctx context.Context, name string) (Artifact, error) {
	if !archiveName.MatchString(name) {
		return Artifact{}, RepositoryError("unmanaged_backup")
	}
	readRegular := func(name string) (*os.File, error) {
		info, err := r.root.Lstat(name)
		if err != nil || !info.Mode().IsRegular() {
			return nil, RepositoryError("unmanaged_backup")
		}
		file, err := r.root.Open(name)
		if err != nil {
			return nil, RepositoryError("unmanaged_backup")
		}
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			file.Close()
			return nil, RepositoryError("unmanaged_backup")
		}
		return file, nil
	}
	manifest, err := readRegular(name + ".json")
	if err != nil {
		return Artifact{}, err
	}
	data, err := io.ReadAll(io.LimitReader(manifest, 8193))
	manifest.Close()
	var signed signedArtifact
	if err != nil || len(data) > 8192 || json.Unmarshal(data, &signed) != nil {
		return Artifact{}, RepositoryError("unmanaged_backup")
	}
	a := signed.Artifact
	if a.Version != 1 || a.OwnerID != r.owner.ID || a.FileName != name || !a.ArchiveVerified || a.Bytes < 1 || a.CompletedAt.IsZero() {
		return Artifact{}, RepositoryError("unmanaged_backup")
	}
	canonical, _ := json.Marshal(a)
	mac := hmac.New(sha256.New, r.key)
	mac.Write(canonical)
	provided, err := hex.DecodeString(signed.Signature)
	if err != nil || !hmac.Equal(mac.Sum(nil), provided) {
		return Artifact{}, RepositoryError("unmanaged_backup")
	}
	file, err := readRegular(name)
	if err != nil {
		return Artifact{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() != a.Bytes {
		return Artifact{}, RepositoryError("backup_file_changed")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, contextReader{ctx, file}); err != nil {
		return Artifact{}, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != a.SHA256 {
		return Artifact{}, RepositoryError("backup_file_changed")
	}
	return a, nil
}

func (r *Repository) inventory(ctx context.Context) ([]Artifact, error) {
	directory, err := r.root.Open(".")
	if err != nil {
		return nil, RepositoryError("backup_inventory_unavailable")
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, RepositoryError("backup_inventory_unavailable")
	}
	artifacts := []Artifact{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !archiveName.MatchString(entry.Name()) {
			continue
		}
		a, err := r.artifact(ctx, entry.Name())
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		artifacts = append(artifacts, a)
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].CompletedAt.After(artifacts[j].CompletedAt) })
	return artifacts, nil
}

func (r *Repository) Inventory(ctx context.Context) ([]Artifact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inventory(ctx)
}

// Prune only removes authenticated, unchanged regular artifacts under the root.
// Unrecognized, tampered, symlinked or partial files are never swept by glob.
func (r *Repository) Prune(ctx context.Context, preserveIDs ...string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	artifacts, err := r.inventory(ctx)
	if err != nil {
		return 0, err
	}
	keep := map[string]bool{}
	for _, id := range preserveIDs {
		found := false
		for _, a := range artifacts {
			if a.ID == id {
				found = true
				break
			}
		}
		if !found {
			return 0, RepositoryError("backup_cleanup_failed")
		}
		keep[id] = true
	}
	if len(keep) > r.keep {
		return 0, RepositoryError("backup_cleanup_failed")
	}
	for _, a := range artifacts {
		if len(keep) >= r.keep {
			break
		}
		keep[a.ID] = true
	}
	removed := 0
	for i := 0; i < len(artifacts); i++ {
		if keep[artifacts[i].ID] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		a, err := r.artifact(ctx, artifacts[i].FileName)
		if err != nil {
			continue
		}
		if err := r.root.Remove(a.FileName); err != nil {
			return removed, RepositoryError("backup_cleanup_failed")
		}
		if err := r.root.Remove(a.FileName + ".json"); err != nil {
			return removed, RepositoryError("backup_cleanup_failed")
		}
		removed++
	}
	return removed, nil
}
