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
	mu        sync.Mutex
	file      *os.File
	written   int64
	discarded int64
	errors    int
	closed    bool
}

func openJointProcessLog(directory string) (*jointProcessLog, error) {
	if directory == "" {
		return nil, nil
	}
	if runtime.GOOS == "windows" {
		return nil, errors.New("private process capture requires POSIX directory permissions")
	}
	if !filepath.IsAbs(directory) {
		return nil, errors.New("process log directory must be absolute")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("process log directory must already exist without a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("process log directory must exclude group and other access")
	}
	file, err := os.OpenFile(filepath.Join(directory, "server-"+rand.Text()+".log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, errors.New("cannot create private process log")
	}
	return &jointProcessLog{file: file}, nil
}

func (p *jointProcessLog) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	if !p.closed && p.written < jointProcessLogLimit {
		var err error
		n, err = p.file.Write(data[:min(len(data), jointProcessLogLimit-int(p.written))])
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

func (p *jointProcessLog) close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		if err := p.file.Close(); err != nil {
			p.errors++
		}
		p.closed = true
	}
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
}

func (p *jointProcessLog) recordStartup(class string, elapsed time.Duration, exited bool, exitCode *int) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	evidence := jointStartupEvidence{Format: "myprobe.joint-startup.v1", Class: class, ElapsedSeconds: elapsed.Seconds(),
		LogFile: filepath.Base(p.file.Name()), LogBytes: p.written, DiscardedBytes: p.discarded, LogWriteErrors: p.errors,
		ExitedBeforeEnd: exited, ExitCode: exitCode}
	name := p.file.Name() + ".startup.json"
	p.mu.Unlock()
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(evidence)
}
