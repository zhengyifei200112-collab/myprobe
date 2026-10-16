package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

const capacityPassphrase = "synthetic-large-backup-passphrase"
const capacityApplicationKey = "synthetic-capacity-application-key-never-production"

// Contains a disposable token. Capacity runs keep it in t.TempDir; portability
// exports it only in the explicit private transfer bundle. Never copy it into
// public evidence, logs or uploaded CI artifacts.
type backupCapacityControl struct {
	NodeID, AgentToken                                   string
	Rows, SourceBytes                                    int64
	MetricDigest, LedgerDigest, RequestID, PreviewDigest string
	Bundle                                               store.ServiceAlertBundle
	Mapping                                              store.ServiceAlertImportMapping
}

func readBackupCapacityControl(t *testing.T, root string) backupCapacityControl {
	t.Helper()
	var c backupCapacityControl
	raw, err := os.ReadFile(filepath.Join(root, "control.json"))
	if err != nil || json.Unmarshal(raw, &c) != nil {
		t.Fatal("private fixture control unavailable", err)
	}
	return c
}

func capacityInspection(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func capacityRowsDigest(ctx context.Context, db *sql.DB, query string) (string, int64, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	h := sha256.New()
	var count int64
	for rows.Next() {
		var a, b string
		if err = rows.Scan(&a, &b); err != nil {
			return "", 0, err
		}
		fmt.Fprintf(h, "%d:", len(a))
		io.WriteString(h, a)
		fmt.Fprintf(h, "%d:", len(b))
		io.WriteString(h, b)
		count++
	}
	return fmt.Sprintf("%x", h.Sum(nil)), count, rows.Err()
}

func seedBackupCapacity(t *testing.T, root string, mib int) {
	ctx := context.Background()
	path := filepath.Join(root, "source.db")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("refusing to seed an existing database")
	}
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Synthetic encrypted capacity observer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = auth.New(db, time.Hour).Bootstrap(ctx, "capacity-admin", "synthetic-capacity-admin-passphrase"); err != nil {
		t.Fatal(err)
	}
	service := alerts.New(db, capacityApplicationKey, nil, nil)
	channel, err := service.CreateChannel(ctx, "Synthetic encrypted destination", "webhook", alerts.ChannelConfig{URL: "https://example.test/synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := db.SaveHTTPService(ctx, store.HTTPService{Name: "Synthetic recovery service", Enabled: false, IntervalSeconds: 60, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "http://example.test/recovery", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	c := backupCapacityControl{NodeID: node.ID, AgentToken: token, RequestID: "synthetic-capacity-replay"}
	c.Bundle = store.ServiceAlertBundle{Format: "myprobe-service-alerts", Version: 1, Channels: []store.ServiceAlertChannelReference{{ID: "source-channel", Name: "Synthetic", Provider: "webhook"}}, Alerts: []store.PortableServiceAlert{{SourceID: "source-service", Name: "Synthetic", ChannelID: "source-channel", Settings: store.ServiceAlertParameters{CooldownSeconds: 300}}}}
	c.Mapping = store.ServiceAlertImportMapping{Services: map[string]string{"source-service": target.ID}, Channels: map[string]string{"source-channel": channel.ID}}
	preview, err := db.PreviewServiceAlertImport(ctx, c.Bundle, c.Mapping)
	if err != nil {
		t.Fatal(err)
	}
	c.PreviewDigest = preview.Digest
	if result, err := db.ApplyServiceAlertImport(ctx, c.RequestID, c.Bundle, c.Mapping, c.PreviewDigest); err != nil || result.Created != 1 || result.Replayed {
		t.Fatal("real import receipt not seeded", err)
	}
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	report := v1.Report{CapturedAt: anchor, CPU: v1.CPUMetric{UsagePercent: 17}, Memory: v1.MemoryMetric{TotalBytes: 2 << 30, UsedBytes: 256 << 20, UsagePercent: 12.5}}
	for i := range 64 {
		report.Networks = append(report.Networks, v1.NetworkMetric{Interface: fmt.Sprintf("synthetic-interface-%03d", i)})
	}
	if err = report.Validate(); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(report)
	if err != nil || len(payload) > v1.MaxMessageBytes {
		t.Fatal("invalid synthetic report payload")
	}
	if err = db.SaveReport(ctx, node.ID, report); err != nil {
		t.Fatal(err)
	}
	inspection := capacityInspection(t, path)
	c.Rows = (int64(mib)<<20)/int64(len(payload)) + 1
	for offset := int64(1); offset < c.Rows; offset += 500 {
		n := min(int64(500), c.Rows-offset)
		// Valid report JSON is expanded in bounded SQL batches. No unused BLOB,
		// sparse file or deleted/free pages stand in for meaningful database data.
		_, err = inspection.Exec(`WITH RECURSIVE seq(n) AS (VALUES(0) UNION ALL SELECT n+1 FROM seq WHERE n+1<?)
INSERT INTO metric_samples(node_id,captured_at,cpu_usage,memory_used,memory_total,disk_used,disk_total,net_rx_rate,net_tx_rate,net_rx_total,net_tx_total,report_json)
SELECT ?,strftime('%Y-%m-%dT%H:%M:%SZ',?+(?+n)*15,'unixepoch'),17,268435456,2147483648,0,0,0,0,0,0,
json_set(?,'$.captured_at',strftime('%Y-%m-%dT%H:%M:%SZ',?+(?+n)*15,'unixepoch')) FROM seq`, n, node.ID, anchor.Unix(), offset, string(payload), anchor.Unix(), offset)
		if err != nil {
			t.Fatal(err)
		}
	}
	c.MetricDigest, _, err = capacityRowsDigest(ctx, inspection, `SELECT captured_at,report_json FROM metric_samples ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	c.LedgerDigest, _, err = capacityRowsDigest(ctx, inspection, `SELECT request_id,request_digest||result_json FROM service_alert_imports ORDER BY request_id`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = inspection.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() < int64(mib)<<20 {
		t.Fatal("fixture did not reach requested live database bytes", err)
	}
	c.SourceBytes = info.Size()
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "control.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func generateBackupCapacity(t *testing.T, root string) {
	ctx := context.Background()
	path := filepath.Join(root, "source.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r, err := OpenRepository(filepath.Join(root, "archives"), path, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	a, err := r.Generate(ctx, db, capacityPassphrase, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	control := readBackupCapacityControl(t, root)
	if !a.ArchiveVerified || a.RecoveryVerified || a.Bytes < control.SourceBytes/2 {
		t.Fatal("incomplete encrypted snapshot")
	}
	if _, _, err = db.CreateNode(ctx, store.CreateNodeParams{Name: "After frozen backup"}); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "artifact.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func recoverBackupCapacity(t *testing.T, root string) {
	ctx := context.Background()
	path := filepath.Join(root, "source.db")
	c := readBackupCapacityControl(t, root)
	r, err := OpenRepository(filepath.Join(root, "archives"), path, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var a Artifact
	raw, err := os.ReadFile(filepath.Join(root, "artifact.json"))
	if err != nil || json.Unmarshal(raw, &a) != nil {
		t.Fatal("missing archive")
	}
	verify := alerts.New(nil, capacityApplicationKey, nil, nil)
	if _, err = r.VerifyRecovery(ctx, a.FileName, capacityPassphrase+"-wrong", verify.VerifyRecoveryChannels); err == nil {
		t.Fatal("wrong archive passphrase accepted")
	}
	wrong := alerts.New(nil, strings.Repeat("wrong-key-", 4), nil, nil)
	if _, err = r.VerifyRecovery(ctx, a.FileName, capacityPassphrase, wrong.VerifyRecoveryChannels); err != RepositoryError("recovery_application_key_unavailable") {
		t.Fatal("wrong application key accepted", err)
	}
	unchanged, err := r.artifact(ctx, a.FileName)
	if err != nil || unchanged.RecoveryVerified {
		t.Fatal("failed recovery upgraded completion metadata", err)
	}
	a, err = r.VerifyRecovery(ctx, a.FileName, capacityPassphrase, func(ctx context.Context, recovered *store.Store) error {
		if err := verify.VerifyRecoveryChannels(ctx, recovered); err != nil {
			return err
		}
		return verifyBackupCapacityState(ctx, recovered, c)
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "artifact.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func verifyBackupCapacityState(ctx context.Context, recovered *store.Store, c backupCapacityControl) error {
	node, err := recovered.AuthenticateAgent(ctx, c.AgentToken)
	if err != nil || node.ID != c.NodeID {
		return fmt.Errorf("restored authentication differs")
	}
	state, err := recovered.ReadRecoveredState(ctx)
	if err != nil || state.Nodes != 1 || state.Users != 1 || state.Channels != 1 || state.Reports != c.Rows {
		return fmt.Errorf("restored snapshot population differs: %v", err)
	}
	inspection, err := sql.Open("sqlite", recovered.DatabasePath())
	if err != nil {
		return err
	}
	defer inspection.Close()
	metrics, rows, err := capacityRowsDigest(ctx, inspection, `SELECT captured_at,report_json FROM metric_samples ORDER BY id`)
	if err != nil || metrics != c.MetricDigest || rows != c.Rows {
		return fmt.Errorf("restored report bytes differ: %v", err)
	}
	ledger, rows, err := capacityRowsDigest(ctx, inspection, `SELECT request_id,request_digest||result_json FROM service_alert_imports ORDER BY request_id`)
	if err != nil || ledger != c.LedgerDigest || rows != 1 {
		return fmt.Errorf("restored permanent receipt differs: %v", err)
	}
	replay, err := recovered.ApplyServiceAlertImport(ctx, c.RequestID, c.Bundle, c.Mapping, c.PreviewDigest)
	if err != nil || !replay.Replayed || replay.Created != 1 {
		return fmt.Errorf("restored permanent replay failed: %v", err)
	}
	return nil
}
