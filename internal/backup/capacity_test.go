package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
)

type backupCapacityPhase struct {
	Phase                                            string
	WallSeconds, CPUSeconds                          float64
	SampledPeakRSSBytes, SampledPeakLogicalFileBytes uint64
	Samples, ReadErrors                              int
	MaximumSampleGapSeconds                          float64
}

type backupCapacityResult struct {
	Format, SourceCommit, OS, GoVersion   string
	CPUModel, Filesystem, HostLoadContext string
	LogicalCPUs                           int
	VisibleMemoryBytes, FreeBytesAtStart  uint64
	SourceSHA256                          map[string]string
	RequestedMiB                          int
	SourceBytes                           int64
	Artifact                              Artifact
	Phases                                []backupCapacityPhase
	Limitations                           []string
}

// This is deliberately opt-in: it uses real SQLite snapshots and the production
// repository, takes several GiB of temporary space and samples child RSS.
func TestEncryptedBackupCapacity(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_BACKUP_CAPACITY") != "1" {
		t.Skip("opt-in encrypted large SQLite backup/recovery")
	}
	output := os.Getenv("MYPROBE_TEST_BACKUP_CAPACITY_OUTPUT")
	if output != "" {
		info, err := os.Stat(output)
		if err != nil || !filepath.IsAbs(output) || !info.IsDir() {
			t.Fatal("capacity output must be an existing absolute directory")
		}
		probe, err := os.CreateTemp(output, ".backup-capacity-write-")
		if err != nil {
			t.Fatal("capacity output is not writable", err)
		}
		name := probe.Name()
		closeErr := probe.Close()
		removeErr := os.Remove(name)
		if closeErr != nil || removeErr != nil {
			t.Fatal("capacity output preflight failed", closeErr, removeErr)
		}
	}
	sizes := os.Getenv("MYPROBE_TEST_BACKUP_CAPACITY_MIB")
	if sizes == "" {
		sizes = "256,1024"
	}
	values := strings.Split(sizes, ",")
	if len(values) > 3 {
		t.Fatal("at most three backup sizes")
	}
	for _, value := range values {
		mib, err := strconv.Atoi(value)
		if err != nil || mib < 32 || mib > 4096 {
			t.Fatal("backup size must be 32..4096 MiB")
		}
		t.Run(value+"MiB", func(t *testing.T) {
			root := t.TempDir()
			var identity [16]byte
			if _, err := rand.Read(identity[:]); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".capacity-owned"), []byte(fmt.Sprintf("%x", identity)), 0600); err != nil {
				t.Fatal(err)
			}
			usage, err := disk.Usage(root)
			if err != nil || usage.Free < 8*(uint64(mib)<<20)+(1<<30) {
				t.Fatal("insufficient disposable capacity volume")
			}
			result := backupCapacityResult{Format: "myprobe.encrypted-backup-capacity.v1", SourceCommit: os.Getenv("MYPROBE_TEST_SOURCE_COMMIT"), OS: runtime.GOOS, GoVersion: runtime.Version(), RequestedMiB: mib, Limitations: []string{"synthetic valid reports; quiet single-node database, not live Server load", "50ms child RSS and logical-file samples can miss peaks; file sizes count directory entries including hardlinks, not allocated disk blocks", "each stage has a fresh process; stage time includes startup", "same-host local recovery, not cross-machine failover or the full 24-hour gate"}}
			result.LogicalCPUs, result.Filesystem, result.FreeBytesAtStart = runtime.NumCPU(), usage.Fstype, usage.Free
			result.HostLoadContext = os.Getenv("MYPROBE_TEST_BACKUP_CAPACITY_HOST_CONTEXT")
			if result.SourceCommit == "" {
				result.SourceCommit = "working-tree"
			}
			processors, err := cpu.Info()
			if err != nil || len(processors) == 0 {
				t.Fatal("CPU evidence unavailable", err)
			}
			result.CPUModel = processors[0].ModelName
			memory, err := mem.VirtualMemory()
			if err != nil {
				t.Fatal(err)
			}
			result.VisibleMemoryBytes = memory.Total
			result.SourceSHA256 = map[string]string{}
			for _, path := range []string{"internal/backup/archive.go", "internal/backup/repository.go", "internal/store/backup.go", "internal/backup/capacity_test.go", "internal/backup/capacity_fixture_test.go"} {
				data, err := os.ReadFile(filepath.Join("../..", path))
				if err != nil {
					t.Fatal(err)
				}
				result.SourceSHA256[path] = fmt.Sprintf("%x", sha256.Sum256(data))
			}
			for _, phase := range []string{"seed", "generate", "recover"} {
				observed := runBackupCapacityPhase(t, root, phase, mib)
				result.Phases = append(result.Phases, observed)
				if phase != "seed" && observed.SampledPeakRSSBytes >= 700<<20 {
					t.Fatal("backup/recovery exceeds 700 MiB sampled RSS", observed)
				}
			}
			control := readBackupCapacityControl(t, root)
			result.SourceBytes = control.SourceBytes
			raw, err := os.ReadFile(filepath.Join(root, "artifact.json"))
			if err != nil || json.Unmarshal(raw, &result.Artifact) != nil {
				t.Fatal("missing completed recovery artifact", err)
			}
			if !result.Artifact.ArchiveVerified || !result.Artifact.RecoveryVerified || result.Artifact.RecoveryEvidence == nil || result.Artifact.RecoveryEvidence.Reports != control.Rows {
				t.Fatal("incomplete recovery proof")
			}
			raw, err = json.MarshalIndent(result, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			t.Log("MYPROBE_BACKUP_CAPACITY_RESULT=" + string(raw))
			if output != "" {
				if err = os.WriteFile(filepath.Join(output, fmt.Sprintf("backup-capacity-%d-%s.json", mib, runtime.GOOS)), append(raw, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func runBackupCapacityPhase(t *testing.T, root, phase string, mib int) backupCapacityPhase {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestEncryptedBackupCapacityWorker$", "-test.timeout=12m", "-test.v")
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "MYPROBE_") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "MYPROBE_TEST_BACKUP_CAPACITY_CHILD=1", "MYPROBE_TEST_BACKUP_CAPACITY_ROOT="+root, "MYPROBE_TEST_BACKUP_CAPACITY_PHASE="+phase, "MYPROBE_TEST_BACKUP_CAPACITY_MIB="+strconv.Itoa(mib))
	identity, err := os.ReadFile(filepath.Join(root, ".capacity-owned"))
	if err != nil || len(identity) != 32 {
		t.Fatal("missing disposable fixture ownership")
	}
	cmd.Env = append(cmd.Env, "MYPROBE_TEST_BACKUP_CAPACITY_NONCE="+string(identity))
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	start := time.Now()
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	child, err := process.NewProcess(int32(cmd.Process.Pid))
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	r := backupCapacityPhase{Phase: phase}
	last := time.Now()
	sample := func() {
		now := time.Now()
		r.MaximumSampleGapSeconds = max(r.MaximumSampleGapSeconds, now.Sub(last).Seconds())
		last = now
		memory, err := child.MemoryInfo()
		if err != nil {
			// A process can exit between the timer event and this read. Only an
			// error for a still-live process makes the observation unavailable.
			alive, checkErr := child.IsRunning()
			if checkErr != nil || alive {
				r.ReadErrors++
			}
			return
		}
		r.Samples++
		r.SampledPeakRSSBytes = max(r.SampledPeakRSSBytes, memory.RSS)
		var total uint64
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			info, err := entry.Info()
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			total += uint64(info.Size())
			return nil
		})
		if err != nil {
			r.ReadErrors++
		}
		r.SampledPeakLogicalFileBytes = max(r.SampledPeakLogicalFileBytes, total)
	}
	sample()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			r.WallSeconds = time.Since(start).Seconds()
			r.CPUSeconds = (cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()).Seconds()
			if err != nil {
				t.Fatalf("%s worker failed: %v\n%s", phase, err, log.String())
			}
			if r.Samples == 0 || r.SampledPeakRSSBytes == 0 || r.ReadErrors != 0 {
				t.Fatalf("%s resource observation unavailable: %+v", phase, r)
			}
			return r
		case <-ticker.C:
			sample()
		}
	}
}

func TestEncryptedBackupCapacityWorker(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_BACKUP_CAPACITY_CHILD") != "1" {
		t.Skip("parent-managed disposable child")
	}
	root := os.Getenv("MYPROBE_TEST_BACKUP_CAPACITY_ROOT")
	if !filepath.IsAbs(root) {
		t.Fatal("absolute private fixture root required")
	}
	identity, err := os.ReadFile(filepath.Join(root, ".capacity-owned"))
	if err != nil || len(identity) != 32 || string(identity) != os.Getenv("MYPROBE_TEST_BACKUP_CAPACITY_NONCE") {
		t.Fatal("parent-owned disposable fixture required")
	}
	mib, err := strconv.Atoi(os.Getenv("MYPROBE_TEST_BACKUP_CAPACITY_MIB"))
	if err != nil || mib < 32 || mib > 4096 {
		t.Fatal("invalid child size")
	}
	switch phase := os.Getenv("MYPROBE_TEST_BACKUP_CAPACITY_PHASE"); phase {
	case "seed":
		seedBackupCapacity(t, root, mib)
	case "generate":
		generateBackupCapacity(t, root)
	case "recover":
		recoverBackupCapacity(t, root)
	default:
		t.Fatal("invalid child phase")
	}
}
