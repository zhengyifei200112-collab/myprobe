package agentclient

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const jointProcessLogLimit = 1 << 20

// Raw Server output can contain generated fixture credentials. Capture is
// explicit, bounded, private, and separate from public aggregate artifacts.
type jointProcessLog struct {
	mu         sync.Mutex
	file       *os.File
	written    int64
	discarded  int64
	errors     int
	closed     bool
	limit      int64
	traceGC    bool
	serverPID  int
	serverUnit string
}

func (p *jointProcessLog) bindProcess(pid int, unit string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.serverPID, p.serverUnit = pid, unit
}

func openJointProcessLog(directory string) (*jointProcessLog, error) {
	traceGC, err := jointRuntimeGCTraceSettings(directory)
	if err != nil {
		return nil, err
	}
	if directory == "" {
		return nil, nil
	}
	if err := validateJointPrivateDirectory(directory); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(directory, "server-"+rand.Text()+".log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, errors.New("cannot create private process log")
	}
	limit := int64(jointProcessLogLimit)
	if traceGC {
		limit = jointRuntimeGCLogLimit
	}
	return &jointProcessLog{file: file, limit: limit, traceGC: traceGC}, nil
}

func validateJointPrivateDirectory(directory string) error {
	if runtime.GOOS == "windows" {
		return errors.New("private test output requires POSIX directory permissions")
	}
	if !filepath.IsAbs(directory) {
		return errors.New("private test directory must be absolute")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("private test directory must already exist without a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("private test directory must exclude group and other access")
	}
	return nil
}

func (p *jointProcessLog) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	if !p.closed && p.written < p.limit {
		var err error
		n, err = p.file.Write(data[:min(len(data), int(p.limit-p.written))])
		p.written += int64(n)
		if err != nil {
			p.errors++
		}
	}
	p.discarded += int64(len(data) - n)
	// Do not return a pipe error when the cap or a storage error drops bytes.
	// The private summary records write errors and truncation explicitly.
	return len(data), nil
}

func (p *jointProcessLog) close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	syncErr := p.file.Sync()
	if syncErr != nil {
		p.errors++
	}
	closeErr := p.file.Close()
	if closeErr != nil {
		p.errors++
	}
	p.closed = true
	return errors.Join(syncErr, closeErr, p.recordCloseout())
}

// Called with mu held after the child's pipe has drained and the log is closed.
func (p *jointProcessLog) recordCloseout() error {
	evidence := jointProcessCloseout{Format: "myprobe.joint-process-closeout.v1", LogFile: filepath.Base(p.file.Name()),
		LogLimitBytes: p.limit, RuntimeGCTrace: p.traceGC, LogBytes: p.written,
		DiscardedBytes: p.discarded, LogWriteErrors: p.errors, Closed: p.closed,
		ServerPID: p.serverPID, ServerUnit: p.serverUnit}
	file, err := os.OpenFile(p.file.Name()+".closeout.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	encodeErr := json.NewEncoder(file).Encode(evidence)
	return errors.Join(encodeErr, file.Sync(), file.Close())
}

type jointProcessCloseout struct {
	Format         string `json:"format"`
	LogFile        string `json:"log_file"`
	LogLimitBytes  int64  `json:"log_limit_bytes"`
	RuntimeGCTrace bool   `json:"runtime_gc_trace"`
	LogBytes       int64  `json:"captured_log_bytes"`
	DiscardedBytes int64  `json:"discarded_log_bytes"`
	LogWriteErrors int    `json:"log_write_errors"`
	Closed         bool   `json:"closed"`
	ServerPID      int    `json:"server_pid"`
	ServerUnit     string `json:"server_unit,omitempty"`
}

type jointStartupEvidence struct {
	Format          string  `json:"format"`
	Class           string  `json:"class"`
	ElapsedSeconds  float64 `json:"elapsed_seconds"`
	LogFile         string  `json:"log_file"`
	LogBytes        int64   `json:"captured_log_bytes"`
	DiscardedBytes  int64   `json:"discarded_log_bytes"`
	LogWriteErrors  int     `json:"log_write_errors"`
	ExitedBeforeEnd bool    `json:"exited_before_cleanup"`
	ExitCode        *int    `json:"exit_code,omitempty"`
	LogLimitBytes   int64   `json:"log_limit_bytes"`
	RuntimeGCTrace  bool    `json:"runtime_gc_trace"`
}

func (p *jointProcessLog) recordStartup(class string, elapsed time.Duration, exited bool, exitCode *int) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	evidence := jointStartupEvidence{Format: "myprobe.joint-startup.v1", Class: class, ElapsedSeconds: elapsed.Seconds(),
		LogFile: filepath.Base(p.file.Name()), LogBytes: p.written, DiscardedBytes: p.discarded, LogWriteErrors: p.errors,
		ExitedBeforeEnd: exited, ExitCode: exitCode, LogLimitBytes: p.limit, RuntimeGCTrace: p.traceGC}
	name := p.file.Name() + ".startup.json"
	p.mu.Unlock()
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(evidence)
}
