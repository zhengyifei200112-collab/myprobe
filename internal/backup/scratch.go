package backup

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var scratchName = regexp.MustCompile(`^\.myprobe-(?:backup|recovery)-work-[a-f0-9]{32}$`)
var scratchRecordName = regexp.MustCompile(`^\.myprobe-scratch-[a-f0-9]{32}\.json$`)

type scratchRecord struct {
	Name      string `json:"name"`
	Owner     string `json:"owner"`
	Signature string `json:"signature"`
}

func (r *Repository) scratchSignature(name string) string {
	mac := hmac.New(sha256.New, r.key)
	mac.Write([]byte("myprobe-scratch-v1\x00" + r.owner.ID + "\x00" + r.owner.Source + "\x00" + name))
	return hex.EncodeToString(mac.Sum(nil))
}

// Persist an authenticated cleanup authorization before creating plaintext.
// Unknown files, symlinks and nonempty directories are never recursively swept.
func (r *Repository) newScratch(kind string) (string, string, error) {
	id, err := randomHex(16)
	if err != nil {
		return "", "", RepositoryError("backup_scratch_unavailable")
	}
	name := ".myprobe-" + kind + "-work-" + id
	if !scratchName.MatchString(name) {
		return "", "", RepositoryError("backup_scratch_unavailable")
	}
	path := filepath.Join(r.scratchDirectory, name)
	if err = os.Mkdir(path, 0o700); err != nil {
		return "", "", RepositoryError("backup_scratch_unavailable")
	}
	record := ".myprobe-scratch-" + id + ".json"
	raw, _ := json.Marshal(scratchRecord{name, r.owner.ID, r.scratchSignature(name)})
	file, err := r.root.OpenFile(record, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		os.Remove(path)
		return "", "", RepositoryError("backup_scratch_unavailable")
	}
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		r.root.Remove(record)
		os.Remove(path)
		return "", "", RepositoryError("backup_scratch_unavailable")
	}
	return path, record, nil
}
func (r *Repository) cleanupScratch(recordName string) error {
	if !scratchRecordName.MatchString(recordName) {
		return RepositoryError("unmanaged_backup_scratch")
	}
	info, err := r.root.Lstat(recordName)
	if err != nil || !info.Mode().IsRegular() {
		return RepositoryError("unmanaged_backup_scratch")
	}
	file, err := r.root.Open(recordName)
	if err != nil {
		return RepositoryError("unmanaged_backup_scratch")
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		file.Close()
		return RepositoryError("unmanaged_backup_scratch")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 4097))
	file.Close()
	var record scratchRecord
	if err != nil || len(raw) > 4096 || json.Unmarshal(raw, &record) != nil || !scratchName.MatchString(record.Name) || record.Owner != r.owner.ID {
		return RepositoryError("unmanaged_backup_scratch")
	}
	provided, err := hex.DecodeString(record.Signature)
	expected, _ := hex.DecodeString(r.scratchSignature(record.Name))
	if err != nil || !hmac.Equal(provided, expected) {
		return RepositoryError("unmanaged_backup_scratch")
	}
	parent, err := os.OpenRoot(r.scratchDirectory)
	if err != nil {
		return RepositoryError("backup_scratch_cleanup_failed")
	}
	defer parent.Close()
	dirInfo, err := parent.Lstat(record.Name)
	if errors.Is(err, os.ErrNotExist) {
		return r.root.Remove(recordName)
	}
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return RepositoryError("backup_scratch_cleanup_failed")
	}
	dir, err := parent.OpenRoot(record.Name)
	if err != nil {
		return RepositoryError("backup_scratch_cleanup_failed")
	}
	handle, err := dir.Open(".")
	if err != nil {
		dir.Close()
		return RepositoryError("backup_scratch_cleanup_failed")
	}
	opened, err = handle.Stat()
	handle.Close()
	if err != nil || !os.SameFile(dirInfo, opened) {
		dir.Close()
		return RepositoryError("backup_scratch_cleanup_failed")
	}
	for _, name := range []string{"snapshot.db", "snapshot.db-wal", "snapshot.db-shm", "verified.db", "verified.db-wal", "verified.db-shm", "recovered.db", "recovered.db-wal", "recovered.db-shm"} {
		info, e := dir.Lstat(name)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil || !info.Mode().IsRegular() {
			dir.Close()
			return RepositoryError("backup_scratch_cleanup_failed")
		}
		if e = dir.Remove(name); e != nil {
			dir.Close()
			return RepositoryError("backup_scratch_cleanup_failed")
		}
	}
	dir.Close()
	if err = parent.Remove(record.Name); err != nil {
		return RepositoryError("backup_scratch_cleanup_failed")
	}
	if err = r.root.Remove(recordName); err != nil {
		return RepositoryError("backup_scratch_cleanup_failed")
	}
	return nil
}
func (r *Repository) recoverScratch() error {
	directory, err := r.root.Open(".")
	if err != nil {
		return RepositoryError("backup_scratch_cleanup_failed")
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return RepositoryError("backup_scratch_cleanup_failed")
	}
	for _, entry := range entries {
		if !scratchRecordName.MatchString(entry.Name()) {
			continue
		}
		err = r.cleanupScratch(entry.Name())
		if err != nil && err != RepositoryError("unmanaged_backup_scratch") {
			return err
		}
	}
	return nil
}
