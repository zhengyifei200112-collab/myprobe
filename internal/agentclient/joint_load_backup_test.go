package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/backup"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

const jointBackupAPI = "/api/v1/admin/maintenance/backup-schedule"

// Aggregate-only evidence: no directory, passphrase, cookie, token or artifact
// filename from the authenticated management response is serialized here.
type jointBackupEvidence struct {
	BackupWindowStart, RecoveryWindowStart                     time.Time
	GuaranteedRetainedSeedReports                              int64
	RequestedAt, ArchiveCompletedAt, RecoveryCompletedAt       time.Time
	ArchiveBytes                                               int64
	Recovered                                                  store.BackupRecoveryEvidence
	BackupACKs, BackupACKNodes, RecoveryACKs, RecoveryACKNodes int
	CompletionPreservedAfterRestart                            bool
}

type jointBackupController struct {
	client                                     *http.Client
	base, csrf, runID                          string
	requested, recovering, complete, restarted bool
	backupStart, recoveryStart                 time.Time
	artifact                                   backup.Artifact
	evidence                                   jointBackupEvidence
}

func prepareJointBackup(root string) (string, error) {
	path := filepath.Join(root, "independent-backup-passphrase")
	if err := os.WriteFile(path, []byte("synthetic-joint-backup-passphrase"), 0600); err != nil {
		return "", err
	}
	return "MYPROBE_BACKUP_PASSPHRASE_FILE=" + path, nil
}

func newJointBackupController(ctx context.Context, client *http.Client, base, csrf, root, channel string) (*jointBackupController, error) {
	b := &jointBackupController{client: client, base: base, csrf: csrf}
	zone := "UTC"
	hour := time.Now().UTC().Hour()
	if hour >= 20 {
		zone = "Etc/GMT+12"
	} else if hour < 4 {
		zone = "Etc/GMT-12"
	}
	config := store.BackupSchedule{Enabled: true, Directory: filepath.Join(root, "online-archives"), DailyTime: "23:59", Timezone: zone, Keep: 2, FailureChannelIDs: []string{channel}}
	if err := b.mutate(ctx, http.MethodPut, "", config, http.StatusOK); err != nil {
		return nil, err
	}
	status, err := b.status(ctx)
	if err != nil {
		return nil, err
	}
	if !status.PassphraseReady || !status.Runtime.Running || len(status.Runs) != 0 {
		return nil, fmt.Errorf("fresh live backup worker not ready")
	}
	return b, nil
}

func (b *jointBackupController) mutate(ctx context.Context, method, suffix string, payload any, want int) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, b.base+jointBackupAPI+suffix, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", b.csrf)
	response, err := b.client.Do(request)
	if err != nil {
		return fmt.Errorf("live backup management request failed: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != want {
		return fmt.Errorf("live backup management status %d, expected %d", response.StatusCode, want)
	}
	return nil
}

func (b *jointBackupController) status(ctx context.Context) (backup.Status, error) {
	var status backup.Status
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, b.base+jointBackupAPI, nil)
	if err != nil {
		return status, err
	}
	response, err := b.client.Do(request)
	if err != nil {
		return status, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return status, fmt.Errorf("live backup status response %d", response.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status); err != nil {
		return status, err
	}
	if status.Runtime.ErrorCode != "" {
		return status, fmt.Errorf("live backup worker failure: %s", status.Runtime.ErrorCode)
	}
	return status, nil
}

func (b *jointBackupController) tick(ctx context.Context, elapsed time.Duration, emit func(context.Context) error) error {
	if !b.requested {
		if elapsed < 10*time.Second {
			return nil
		}
		b.evidence.RequestedAt = time.Now().UTC()
		if err := b.mutate(ctx, http.MethodPost, "/run", struct{}{}, http.StatusAccepted); err != nil {
			return err
		}
		b.backupStart = time.Now()
		b.requested = true
		return emit(ctx) // Real client reports while the asynchronous job is active.
	}
	status, err := b.status(ctx)
	if err != nil {
		return err
	}
	for _, run := range status.Runs {
		if run.Trigger != "manual" {
			return fmt.Errorf("unexpected daily backup in short fixture")
		}
		if b.runID != "" && run.ID != b.runID {
			return fmt.Errorf("unexpected second manual backup")
		}
		b.runID = run.ID
		if run.Status == "failed" || run.Status == "interrupted" {
			return fmt.Errorf("live backup ended %s: %s", run.Status, run.ErrorCode)
		}
		if run.Status != "completed" {
			continue
		}
		var a backup.Artifact
		if err = json.Unmarshal(run.Artifact, &a); err != nil || !a.ArchiveVerified {
			return fmt.Errorf("live archive lacks verification")
		}
		if !b.recovering {
			if a.StartedAt.IsZero() {
				return fmt.Errorf("live archive has no start evidence")
			}
			if a.StartedAt.After(b.backupStart) {
				b.backupStart = a.StartedAt
			}
			b.evidence.BackupWindowStart = b.backupStart.UTC()
			b.artifact = a
			b.evidence.ArchiveCompletedAt = a.CompletedAt
			b.evidence.ArchiveBytes = a.Bytes
			if err = b.mutate(ctx, http.MethodPost, "/runs/"+run.ID+"/verify", struct{}{}, http.StatusAccepted); err != nil {
				return err
			}
			b.recoveryStart = time.Now()
			b.evidence.RecoveryWindowStart = b.recoveryStart.UTC()
			b.recovering = true
			return emit(ctx)
		}
		if a.ID != b.artifact.ID || a.SHA256 != b.artifact.SHA256 {
			return fmt.Errorf("live archive identity changed")
		}
		if a.RecoveryVerified {
			if a.RecoveryVerifiedAt == nil || a.RecoveryEvidence == nil {
				return fmt.Errorf("live recovery has no state evidence")
			}
			b.complete = true
			b.evidence.RecoveryCompletedAt = *a.RecoveryVerifiedAt
			b.evidence.Recovered = *a.RecoveryEvidence
			if b.restarted {
				b.evidence.CompletionPreservedAfterRestart = true
			}
		}
	}
	return nil
}

func (b *jointBackupController) readyForRestart() bool { return b == nil || b.complete }

func (b *jointBackupController) finish(observer *jointReportObserver, nodes int, minimumRows int64) (jointBackupEvidence, error) {
	b.evidence.GuaranteedRetainedSeedReports = minimumRows
	if !b.complete || !b.evidence.CompletionPreservedAfterRestart {
		return b.evidence, fmt.Errorf("backup/recovery/restart evidence incomplete")
	}
	if minimumRows < 1 || b.evidence.Recovered.Nodes != int64(nodes) || b.evidence.Recovered.Users < 1 || b.evidence.Recovered.Channels < 1 || b.evidence.Recovered.Reports < minimumRows {
		return b.evidence, fmt.Errorf("live recovery population differs")
	}
	b.evidence.BackupACKs, b.evidence.BackupACKNodes = observer.acknowledgedDuring(b.backupStart, b.evidence.ArchiveCompletedAt)
	b.evidence.RecoveryACKs, b.evidence.RecoveryACKNodes = observer.acknowledgedDuring(b.recoveryStart, b.evidence.RecoveryCompletedAt)
	if b.evidence.BackupACKs == 0 || b.evidence.RecoveryACKs == 0 {
		return b.evidence, fmt.Errorf("no committed report ACK observed during a backup/recovery operation")
	}
	return b.evidence, nil
}

func (p *jointReportObserver) acknowledgedDuring(start, end time.Time) (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	nodes := map[string]bool{}
	count := 0
	for _, attempt := range p.attempts {
		if !attempt.forwarded || !attempt.acknowledged || attempt.sentAt.Before(start) || attempt.ackAt.Before(attempt.sentAt) || !attempt.ackAt.Before(end) {
			continue
		}
		count++
		nodes[attempt.node] = true
	}
	return count, len(nodes)
}

// The seed is a continuous 15s grid from anchor-7d-15s through its final prime
// report. Startup retention legitimately removes the old prefix. Every seed row
// newer than the archive completion cutoff must still occur in the snapshot;
// concurrent live reports may add more. Never compare against pre-retention N.
func minimumRetainedJointSeedReports(history jointHistorySeed, nodes int, asOf time.Time, retention time.Duration) (int64, error) {
	if nodes < 1 || history.MetricRows < 1 || history.MetricRows%int64(nodes) != 0 || history.Anchor.IsZero() || asOf.IsZero() || retention <= 0 {
		return 0, fmt.Errorf("invalid retained seed population")
	}
	first := history.Anchor.Add(-7*24*time.Hour - 15*time.Second)
	perNode := history.MetricRows / int64(nodes)
	cutoff := asOf.Add(-retention)
	removed := int64(0)
	if cutoff.After(first) {
		delta := cutoff.Sub(first)
		removed = int64(delta / (15 * time.Second))
		if delta%(15*time.Second) != 0 {
			removed++
		}
	}
	return max(int64(0), perNode-removed) * int64(nodes), nil
}
