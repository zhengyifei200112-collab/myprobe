package backup

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/disk"

	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type portableBackupProducer struct {
	Format, SourceCommit, OS, Arch, GoVersion string
	ArchiveSHA256, ControlSHA256              string
	ArchiveBytes, Reports                     int64
	RequestedMiB                              int
	SourceBytes                               int64
	SourceSHA256                              map[string]string
}

type portableBackupRecovery struct {
	Format, SourceCommit, OS, Arch, GoVersion string
	Producer                                  portableBackupProducer
	SourceSHA256                              map[string]string
	CrossPlatform                             bool
	Checks                                    []string
	Limitations                               []string
}

// Export/import are deliberately separate processes. Transfer ONLY the private
// bundle to the other machine; neither a live DB nor repository ownership files
// are needed. The public result contains no token, passphrase or real endpoint.
func TestEncryptedBackupPortability(t *testing.T) {
	mode := os.Getenv("MYPROBE_TEST_BACKUP_PORTABILITY")
	if mode == "" {
		t.Skip("opt-in portable encrypted backup export/import")
	}
	if mode != "export" && mode != "import" && mode != "roundtrip" {
		t.Fatal("portability mode must be export, import or roundtrip")
	}
	mib, err := parsePortableMiB(os.Getenv("MYPROBE_TEST_BACKUP_PORTABILITY_MIB"))
	if err != nil {
		t.Fatal(err)
	}
	source := os.Getenv("MYPROBE_TEST_SOURCE_COMMIT")
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(source) {
		t.Fatal("full source commit required; bind the test binary separately in the run manifest")
	}
	bundle := os.Getenv("MYPROBE_TEST_BACKUP_PORTABILITY_BUNDLE")
	if mode == "roundtrip" {
		bundle = t.TempDir()
	}
	requirePortableDirectory(t, bundle)
	if mode != "import" {
		exportPortableBackup(t, bundle, source, mib)
	}
	if mode != "export" {
		output := os.Getenv("MYPROBE_TEST_BACKUP_PORTABILITY_OUTPUT")
		requirePortableDirectory(t, output)
		importPortableBackup(t, bundle, output, source, mib)
	}
}

func parsePortableMiB(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 32, nil
	}
	mib, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || (mib != 32 && mib != 512 && mib != 1024) {
		return 0, fmt.Errorf("portable size must be 32, 512 or 1024 MiB")
	}
	return mib, nil
}

func portableArchiveLimit(mib int) int64 { return (int64(mib)*2 + 64) << 20 }

func requirePortableSpace(t *testing.T, root string, mib int) {
	t.Helper()
	usage, err := disk.Usage(root)
	if err != nil || usage.Free < 8*(uint64(mib)<<20)+(1<<30) {
		t.Fatal("insufficient disposable space for portable archive and restore copies")
	}
}

func requirePortableDirectory(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || !filepath.IsAbs(path) || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("portability directory must exist, be absolute and not be a symlink")
	}
}

func portableSourceHashes(t *testing.T) map[string]string {
	t.Helper()
	hashes := map[string]string{}
	for _, name := range []string{"internal/backup/archive.go", "internal/backup/repository.go", "internal/store/backup.go", "internal/backup/capacity_fixture_test.go", "internal/backup/portability_test.go"} {
		_, hashes[name] = portableFileHash(t, filepath.Join("../..", name))
	}
	return hashes
}

func portableFileHash(t *testing.T, path string) (int64, string) {
	t.Helper()
	return portableFileHashLimit(t, path, 128<<20)
}

func portableFileHashLimit(t *testing.T, path string, limit int64) (int64, string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		t.Fatal("portable input must be a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal("cannot open portable input")
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil || n != info.Size() {
		t.Fatal("portable input hash unavailable")
	}
	return n, fmt.Sprintf("%x", h.Sum(nil))
}

func writePortableJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writePortableFile(t, path, strings.NewReader(string(raw)+"\n"))
}

func writePortableFile(t *testing.T, path string, r io.Reader) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("refusing to replace a portable output")
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	if err != nil || f.Sync() != nil {
		t.Fatal("cannot finalize portable output")
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
}

func exportPortableBackup(t *testing.T, bundle, source string, mib int) {
	t.Helper()
	entries, err := os.ReadDir(bundle)
	if err != nil || len(entries) != 0 {
		t.Fatal("export requires a fresh empty private bundle directory")
	}
	root := t.TempDir()
	requirePortableSpace(t, root, mib)
	requirePortableSpace(t, bundle, mib)
	seedBackupCapacity(t, root, mib)
	generateBackupCapacity(t, root)
	var artifact Artifact
	raw, err := os.ReadFile(filepath.Join(root, "artifact.json"))
	if err != nil || json.Unmarshal(raw, &artifact) != nil || !artifact.ArchiveVerified {
		t.Fatal("verified encrypted archive unavailable")
	}
	for _, item := range []struct{ source, destination string }{
		{filepath.Join(root, "archives", artifact.FileName), "archive.mpb"},
		{filepath.Join(root, "control.json"), "control.json"},
	} {
		f, err := os.Open(item.source)
		if err != nil {
			t.Fatal("private export input unavailable")
		}
		writePortableFile(t, filepath.Join(bundle, item.destination), f)
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	c := readBackupCapacityControl(t, root)
	n, digest := portableFileHashLimit(t, filepath.Join(bundle, "archive.mpb"), portableArchiveLimit(mib))
	_, controlDigest := portableFileHash(t, filepath.Join(bundle, "control.json"))
	producer := portableBackupProducer{Format: "myprobe.backup-portability-producer.v2", SourceCommit: source, OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version(), ArchiveSHA256: digest, ControlSHA256: controlDigest, ArchiveBytes: n, Reports: c.Rows, RequestedMiB: mib, SourceBytes: c.SourceBytes, SourceSHA256: portableSourceHashes(t)}
	writePortableJSON(t, filepath.Join(bundle, "producer.json"), producer)
	t.Logf("portable export: %s/%s reports=%d encrypted_bytes=%d sha256=%s", runtime.GOOS, runtime.GOARCH, c.Rows, n, digest)
}

func readPortableJSON(t *testing.T, path string, destination any) {
	t.Helper()
	n, _ := portableFileHash(t, path)
	if n <= 0 || n > 64<<10 {
		t.Fatal("portable descriptor must be at most 64 KiB")
	}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, destination) != nil {
		t.Fatal("invalid portable descriptor")
	}
}

func importPortableBackup(t *testing.T, bundle, output, source string, mib int) {
	t.Helper()
	ctx := context.Background()
	var producer portableBackupProducer
	readPortableJSON(t, filepath.Join(bundle, "producer.json"), &producer)
	origin := os.Getenv("MYPROBE_TEST_BACKUP_PORTABILITY_ORIGIN")
	if os.Getenv("MYPROBE_TEST_BACKUP_PORTABILITY") == "roundtrip" {
		origin = runtime.GOOS + "/" + runtime.GOARCH
	}
	hashes := portableSourceHashes(t)
	if err := validatePortableProducer(producer, source, origin, hashes, mib); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(output, "recovery.json")); !os.IsNotExist(err) {
		t.Fatal("recovery result already exists or cannot be inspected")
	}
	archivePath := filepath.Join(bundle, "archive.mpb")
	n, archiveHash := portableFileHashLimit(t, archivePath, portableArchiveLimit(mib))
	_, controlHash := portableFileHash(t, filepath.Join(bundle, "control.json"))
	if n != producer.ArchiveBytes || n < int64(mib)<<20 || archiveHash != producer.ArchiveSHA256 || controlHash != producer.ControlSHA256 {
		t.Fatal("portable transfer size/digest mismatch")
	}
	var c backupCapacityControl
	readPortableJSON(t, filepath.Join(bundle, "control.json"), &c)
	if c.Rows != producer.Reports || c.SourceBytes != producer.SourceBytes {
		t.Fatal("portable report count mismatch")
	}
	archive, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if err = Decrypt(io.Discard, archive, capacityPassphrase+"-wrong"); err == nil {
		t.Fatal("wrong passphrase accepted")
	}
	if _, err = archive.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if err = Decrypt(io.Discard, io.LimitReader(archive, n-1), capacityPassphrase); err == nil {
		t.Fatal("truncated transferred archive accepted")
	}
	if _, err = archive.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	requirePortableSpace(t, root, mib)
	plain := filepath.Join(root, "validated.db")
	writePortableDecryption(t, plain, archive)
	check, err := store.Open(ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	wrong := alerts.New(nil, strings.Repeat("wrong-key-", 4), nil, nil)
	if err = wrong.VerifyRecoveryChannels(ctx, check); err == nil {
		t.Fatal("wrong application key accepted")
	}
	verify := alerts.New(nil, capacityApplicationKey, nil, nil)
	if err = verify.VerifyRecoveryChannels(ctx, check); err != nil {
		t.Fatal("transferred channel credentials unavailable")
	}
	if err = check.Close(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "destination.db")
	previous, err := store.Open(ctx, destination)
	if err != nil {
		t.Fatal(err)
	}
	_, previousToken, err := previous.CreateNode(ctx, store.CreateNodeParams{Name: "Disposable pre-restore node"})
	if err != nil {
		t.Fatal(err)
	}
	if err = previous.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = store.StageDatabaseRestore(ctx, destination, plain); err != nil {
		t.Fatal(err)
	}
	rollback, err := store.ApplyPendingRestore(ctx, destination, time.Now())
	if err != nil || rollback == "" {
		t.Fatal("staged restore did not preserve previous database", err)
	}
	recovered, err := store.Open(ctx, destination)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if err = verifyBackupCapacityState(ctx, recovered, c); err != nil {
		t.Fatal(err)
	}
	if _, _, err = auth.New(recovered, time.Hour).Login(ctx, "capacity-admin", "synthetic-capacity-admin-passphrase"); err != nil {
		t.Fatal("restored administrator login failed")
	}
	if _, err = recovered.AuthenticateAgent(ctx, previousToken); err == nil {
		t.Fatal("destination-only token survived replacement")
	}
	old, err := store.Open(ctx, rollback)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if _, err = old.AuthenticateAgent(ctx, previousToken); err != nil {
		t.Fatal("previous database was not preserved")
	}
	if _, err = old.AuthenticateAgent(ctx, c.AgentToken); err == nil {
		t.Fatal("rollback database unexpectedly contains source credentials")
	}
	result := portableBackupRecovery{Format: "myprobe.backup-portability-recovery.v2", SourceCommit: source, OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version(), Producer: producer, SourceSHA256: hashes, CrossPlatform: producer.OS != runtime.GOOS || producer.Arch != runtime.GOARCH,
		Checks:      []string{"encrypted transfer SHA-256/size", "wrong passphrase rejected", "truncation rejected", "wrong application key rejected", "channel decryption with separate application key", "production staged restore and activation", "snapshot population and exact report digest", "permanent import receipt digest and replay", "Agent authentication", "administrator login", "previous destination preserved and credentials isolated"},
		Limitations: []string{fmt.Sprintf("synthetic %d MiB live-report payload target; actual source/archive bytes recorded separately; no production data or external notification requests", mib), "same-platform roundtrip is not cross-machine proof; bind transport, machine and binary identity in a separate run manifest", "Store restore path only, not an HTTP upload, Server restart or production failover", "source commit is declared by the runner; source hashes and executable digests must be independently bound"}}
	writePortableJSON(t, filepath.Join(output, "recovery.json"), result)
	t.Logf("portable restore passed: %s/%s -> %s/%s reports=%d", producer.OS, producer.Arch, runtime.GOOS, runtime.GOARCH, producer.Reports)
}

func validatePortableProducer(p portableBackupProducer, source, origin string, hashes map[string]string, mib int) error {
	if p.Format != "myprobe.backup-portability-producer.v2" || p.Reports <= 0 || p.SourceCommit != source || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(p.SourceCommit) {
		return fmt.Errorf("portable producer format, source or population mismatch")
	}
	if (mib != 32 && mib != 512 && mib != 1024) || p.RequestedMiB != mib || p.SourceBytes < int64(mib)<<20 || p.SourceBytes > portableArchiveLimit(mib) || p.ArchiveBytes < int64(mib)<<20 || p.ArchiveBytes > portableArchiveLimit(mib) {
		return fmt.Errorf("portable producer size profile mismatch")
	}
	if origin == "" || p.OS == "" || p.Arch == "" || p.OS+"/"+p.Arch != origin {
		return fmt.Errorf("explicit expected producer OS/architecture required")
	}
	if len(p.SourceSHA256) != len(hashes) || len(hashes) == 0 {
		return fmt.Errorf("portable source hash set differs")
	}
	for name, digest := range hashes {
		if p.SourceSHA256[name] != digest || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(digest) {
			return fmt.Errorf("portable source hash differs")
		}
	}
	return nil
}

func TestPortableBackupProducerIdentity(t *testing.T) {
	source, digest := strings.Repeat("a", 40), strings.Repeat("b", 64)
	hashes := map[string]string{"source.go": digest}
	valid := portableBackupProducer{Format: "myprobe.backup-portability-producer.v2", SourceCommit: source, OS: "windows", Arch: "amd64", Reports: 3990, RequestedMiB: 32, SourceBytes: 36 << 20, ArchiveBytes: 36 << 20, SourceSHA256: hashes}
	if err := validatePortableProducer(valid, source, "windows/amd64", hashes, 32); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"different commit", "missing origin", "different origin", "missing source hashes", "changed source hash", "empty population", "different size", "undersized database", "oversized archive", "old descriptor format"} {
		t.Run(name, func(t *testing.T) {
			p, expectedSource, origin := valid, source, "windows/amd64"
			switch name {
			case "different commit":
				expectedSource = strings.Repeat("c", 40)
			case "missing origin":
				origin = ""
			case "different origin":
				origin = "linux/arm64"
			case "missing source hashes":
				p.SourceSHA256 = nil
			case "changed source hash":
				p.SourceSHA256 = map[string]string{"source.go": strings.Repeat("c", 64)}
			case "empty population":
				p.Reports = 0
			case "different size":
				p.RequestedMiB = 512
			case "undersized database":
				p.SourceBytes = (32 << 20) - 1
			case "oversized archive":
				p.ArchiveBytes = portableArchiveLimit(32) + 1
			case "old descriptor format":
				p.Format = "myprobe.backup-portability-producer.v1"
			}
			if err := validatePortableProducer(p, expectedSource, origin, hashes, 32); err == nil {
				t.Fatal("inconsistent producer accepted")
			}
		})
	}
}

func TestPortableBackupSizeProfile(t *testing.T) {
	for _, item := range []struct {
		raw  string
		want int
	}{{"", 32}, {"32", 32}, {" 512 ", 512}, {"1024", 1024}} {
		got, err := parsePortableMiB(item.raw)
		if err != nil || got != item.want {
			t.Fatalf("profile %q: got %d, %v", item.raw, got, err)
		}
	}
	for _, raw := range []string{"-1", "0", "31", "33", "513", "1025", "512.0", "512,1024", "9223372036854775808"} {
		if _, err := parsePortableMiB(raw); err == nil {
			t.Fatalf("invalid profile accepted: %q", raw)
		}
	}
	for _, mib := range []int{32, 512, 1024} {
		p := portableBackupProducer{Format: "myprobe.backup-portability-producer.v2", SourceCommit: strings.Repeat("a", 40), OS: "linux", Arch: "arm64", RequestedMiB: mib, SourceBytes: int64(mib) << 20, ArchiveBytes: int64(mib) << 20, Reports: 1, SourceSHA256: map[string]string{"source.go": strings.Repeat("b", 64)}}
		if err := validatePortableProducer(p, p.SourceCommit, "linux/arm64", p.SourceSHA256, mib); err != nil {
			t.Fatal(err)
		}
		p.SourceBytes = portableArchiveLimit(mib) + 1
		if err := validatePortableProducer(p, p.SourceCommit, "linux/arm64", p.SourceSHA256, mib); err == nil {
			t.Fatal("oversized source accepted")
		}
	}
}

func writePortableDecryption(t *testing.T, path string, archive io.Reader) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = Decrypt(f, archive, capacityPassphrase); err != nil {
		t.Fatal("transferred archive decryption failed")
	}
	if err = f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
}
