package backup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func schedulerFixture(t *testing.T, handler http.HandlerFunc) (*Scheduler, *store.Store, *alerts.Service, string, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	base := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(base, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	receiver := httptest.NewServer(handler)
	t.Cleanup(receiver.Close)
	service := alerts.New(db, strings.Repeat("synthetic-key-", 4), alerts.NewHTTPSender(receiver.Client()), nil)
	channel, err := service.CreateChannel(ctx, "synthetic backup destination", "webhook", alerts.ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	passFile := filepath.Join(base, "independent-passphrase")
	if err = os.WriteFile(passFile, []byte("synthetic-backup-passphrase\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worker, err := NewScheduler(ctx, db, passFile, service.VerifyRecoveryChannels, service.ValidateBackupFailureChannels)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); worker.Close() })
	cfg := store.BackupSchedule{Enabled: true, Directory: filepath.Join(base, "archives"), DailyTime: "03:00", Timezone: "Asia/Shanghai", Keep: 2, FailureChannelIDs: []string{channel.ID}}
	if err = worker.Save(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	return worker, db, service, passFile, channel.ID
}
func TestScheduledBackupLocalDateClaimAndFailureReminder(t *testing.T) {
	var requests atomic.Int64
	worker, db, service, passFile, _ := schedulerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var n alerts.Notification
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			t.Error(err)
		}
		if n.Kind != "backup_failure" || n.NodeID != "" || strings.Contains(n.Message, "source.db") || strings.Contains(n.Message, "synthetic-backup-passphrase") {
			t.Error("invalid or private notification")
		}
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	ctx := context.Background()
	before := time.Date(2026, 10, 14, 18, 59, 0, 0, time.UTC) // Oct 15 02:59 local
	if run, err := worker.Tick(ctx, before, false); err != nil || run != nil {
		t.Fatalf("early claim: %v %v", run, err)
	}
	if err := os.Remove(passFile); err != nil {
		t.Fatal(err)
	}
	at := before.Add(time.Minute)
	run, err := worker.Tick(ctx, at, false)
	if err == nil || run == nil || run.Status != "failed" || run.ErrorCode != "backup_passphrase_unavailable" {
		t.Fatalf("failure: %+v %v", run, err)
	}
	for _, clock := range []time.Time{at, at.Add(time.Hour), at.Add(-24 * time.Hour)} {
		if next, err := worker.Tick(ctx, clock, false); err != nil || next != nil {
			t.Fatalf("repeated/rollback claim: %+v %v", next, err)
		}
	}
	notices, err := db.SystemNotifications(ctx)
	if err != nil || len(notices) != 1 || notices[0].Status != "pending" {
		t.Fatalf("durable reminder: %+v %v", notices, err)
	}
	if worked, err := service.DeliverSystemNotification(ctx, at.Add(time.Second)); err != nil || !worked {
		t.Fatalf("actual delivery: %v %v", worked, err)
	}
	notices, err = db.SystemNotifications(ctx)
	if err != nil || notices[0].Status != "delivered" || requests.Load() != 1 {
		t.Fatalf("confirmation: %+v %v", notices, err)
	}
	status, err := worker.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), passFile) || strings.Contains(string(raw), "synthetic-key-") || strings.Contains(string(raw), "synthetic-backup-passphrase") {
		t.Fatal("status leaked key material")
	}
}

func TestScheduledBackupIsolatedRecoveryRequiresOriginalMaterials(t *testing.T) {
	worker, db, service, passFile, _ := schedulerFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("recovery sent an external notification") })
	ctx := context.Background()
	if _, err := auth.New(db, time.Hour).Bootstrap(ctx, "synthetic-admin", "synthetic-admin-password"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "before snapshot"}); err != nil {
		t.Fatal(err)
	}
	run, err := worker.Tick(ctx, time.Now().UTC(), true)
	if err != nil || run == nil || run.Status != "completed" {
		t.Fatalf("backup: %+v %v", run, err)
	}
	if _, _, err = db.CreateNode(ctx, store.CreateNodeParams{Name: "after snapshot"}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(passFile, []byte("different-invalid-passphrase"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.VerifyRecovery(ctx, run.ID); err != RepositoryError("recovery_decryption_failed") {
		t.Fatalf("wrong password: %v", err)
	}
	raw, _ := db.BackupRunArtifact(ctx, run.ID)
	var a Artifact
	json.Unmarshal(raw, &a)
	if a.RecoveryVerified {
		t.Fatal("failed recovery was marked verified")
	}
	if err = os.WriteFile(passFile, []byte("synthetic-backup-passphrase"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrongKey := alerts.New(db, strings.Repeat("different-key-", 4), nil, nil)
	worker.verifyChannels = wrongKey.VerifyRecoveryChannels
	if _, err = worker.VerifyRecovery(ctx, run.ID); err != RepositoryError("recovery_application_key_unavailable") {
		t.Fatalf("wrong application key: %v", err)
	}
	worker.verifyChannels = service.VerifyRecoveryChannels
	a, err = worker.VerifyRecovery(ctx, run.ID)
	if err != nil || !a.RecoveryVerified || a.RecoveryVerifiedAt == nil || a.RecoveryEvidence == nil || a.RecoveryEvidence.Nodes != 1 || a.RecoveryEvidence.Users != 1 || a.RecoveryEvidence.Channels != 1 {
		t.Fatalf("isolated recovery: %+v %v", a, err)
	}
	nodes, err := db.ListNodes(ctx)
	if err != nil || len(nodes) != 2 {
		t.Fatal("exercise changed live database")
	}
	raw, err = db.BackupRunArtifact(ctx, run.ID)
	if err != nil || !strings.Contains(string(raw), `"recovery_verified":true`) {
		t.Fatal("recovery record not persisted")
	}
}

func TestScheduledBackupDSTGapAndRepeatedHour(t *testing.T) {
	for _, test := range []struct {
		name, daily  string
		start, again time.Time
	}{
		{"spring gap", "02:30", time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC), time.Date(2026, 3, 8, 8, 0, 0, 0, time.UTC)},
		{"repeated hour", "01:30", time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC), time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker, db, _, _, _ := schedulerFixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
			cfg, err := db.BackupSchedule(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			cfg.Timezone = "America/New_York"
			cfg.DailyTime = test.daily
			if err = worker.Save(context.Background(), cfg); err != nil {
				t.Fatal(err)
			}
			if run, err := worker.Tick(context.Background(), test.start, false); err != nil || run == nil {
				t.Fatalf("DST claim: %+v %v", run, err)
			}
			if run, err := worker.Tick(context.Background(), test.again, false); err != nil || run != nil {
				t.Fatalf("DST duplicate: %+v %v", run, err)
			}
		})
	}
}

func TestScheduledBackupRestartAndExclusiveWorker(t *testing.T) {
	worker, db, service, passFile, channel := schedulerFixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	ctx := context.Background()
	if other, err := NewScheduler(ctx, db, passFile, service.VerifyRecoveryChannels, service.ValidateBackupFailureChannels); err != RepositoryError("backup_worker_in_use") {
		if other != nil {
			other.Close()
		}
		t.Fatalf("duplicate worker: %v", err)
	}
	at := time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC)
	claimed, err := db.BeginBackupRun(ctx, "2026-10-15", []string{channel}, at)
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewScheduler(ctx, db, passFile, service.VerifyRecoveryChannels, service.ValidateBackupFailureChannels)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	runs, err := db.BackupRuns(ctx)
	if err != nil || len(runs) != 1 || runs[0].ID != claimed.ID || runs[0].Status != "interrupted" {
		t.Fatalf("reconciliation: %+v %v", runs, err)
	}
	if next, err := restarted.Tick(ctx, at, false); err != nil || next != nil {
		t.Fatal("restart reran claimed date")
	}
	notices, err := db.SystemNotifications(ctx)
	if err != nil || len(notices) != 1 {
		t.Fatal("restart failure notification missing")
	}
	if _, err = restarted.Tick(ctx, at, true); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryDiskSpaceAndInterruptedScratchCleanup(t *testing.T) {
	r, db, dir := repositoryFixture(t, 1)
	ctx := context.Background()
	r.freeSpace = func(context.Context, string) (uint64, error) { return 1, nil }
	if _, err := r.Generate(ctx, db, "synthetic-backup-passphrase", time.Now()); err != RepositoryError("backup_insufficient_space") {
		t.Fatalf("space preflight: %v", err)
	}
	r.freeSpace = func(context.Context, string) (uint64, error) { return 1 << 40, nil }
	work, record, err := r.newScratch("backup")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(work, "snapshot.db"), []byte("synthetic sensitive plaintext"), 0o600); err != nil {
		t.Fatal(err)
	}
	unowned := filepath.Join(filepath.Dir(db.DatabasePath()), ".myprobe-backup-work-"+strings.Repeat("f", 32))
	if err = os.Mkdir(unowned, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(unowned, "snapshot.db"), []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenRepository(dir, db.DatabasePath(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, err = os.Stat(work); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owned plaintext scratch not cleaned")
	}
	if _, err = os.Stat(filepath.Join(dir, record)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("scratch record retained")
	}
	if _, err = os.Stat(filepath.Join(unowned, "snapshot.db")); err != nil {
		t.Fatal("foreign scratch removed")
	}
}

func TestScheduledBackupCanDisableAfterChannelDeletion(t *testing.T) {
	worker, db, service, _, channel := schedulerFixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	ctx := context.Background()
	cfg, err := db.BackupSchedule(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.DeleteChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	cfg.Enabled = false
	if err = worker.Save(ctx, cfg); err != nil {
		t.Fatalf("cannot disable after channel deletion: %v", err)
	}
	cfg.Enabled = true
	if err = worker.Save(ctx, cfg); err != RepositoryError("backup_failure_channel_unavailable") {
		t.Fatalf("deleted destination enabled: %v", err)
	}
	saved, err := db.BackupSchedule(ctx)
	if err != nil || saved.Enabled {
		t.Fatal("failed enable changed persisted disabled state")
	}
}
