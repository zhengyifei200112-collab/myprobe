package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

// One real maximal application supplies the exact result bytes and digest.
// SQL copies its receipt to fill the permanent ledger; this is storage/correctness
// evidence, not a claim that ten thousand real 1000-item imports were executed.
func serviceReplayCapacityFixture(t *testing.T, idLength int) (*Store, ServiceAlertBundle, ServiceAlertImportMapping, string, string, ServiceAlertImportResult) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	usage, err := disk.Usage(root)
	if err != nil || usage.Free < 8<<30 {
		t.Fatal("permanent replay fixture requires 8 GiB free on its disposable volume")
	}
	s, err := Open(ctx, filepath.Join(root, "replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic replay observer"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := s.CreateNotificationChannel(ctx, "Synthetic replay destination", "webhook", "synthetic-replay-only")
	if err != nil {
		t.Fatal(err)
	}
	config := ConfigSnapshot{Version: 2}
	bundle := ServiceAlertBundle{Format: "myprobe-service-alerts", Version: 1, Channels: []ServiceAlertChannelReference{{ID: "source-channel", Name: "Synthetic source channel", Provider: "webhook"}}}
	mapping := ServiceAlertImportMapping{Services: map[string]string{}, Channels: map[string]string{"source-channel": channel.ID}}
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("%0*d", idLength, i)
		config.HTTPServices = append(config.HTTPServices, ConfigHTTPService{ID: id, Name: fmt.Sprintf("Synthetic service %04d", i), Enabled: false, IntervalSeconds: 60, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "http://example.test/replay", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
		sourceID := fmt.Sprintf("source-%04d", i)
		bundle.Alerts = append(bundle.Alerts, PortableServiceAlert{SourceID: sourceID, Name: fmt.Sprintf("Synthetic source %04d", i), ChannelID: "source-channel", Settings: ServiceAlertParameters{CooldownSeconds: 300}})
		mapping.Services[sourceID] = id
	}
	if _, err := s.ImportConfig(ctx, config, false); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewServiceAlertImport(ctx, bundle, mapping)
	if err != nil {
		t.Fatal(err)
	}
	requestID := strings.Repeat("r", 120) + "00000000"
	result, err := s.ApplyServiceAlertImport(ctx, requestID, bundle, mapping, preview.Digest)
	if err != nil || result.Replayed || result.Created != 1000 || len(result.ServiceIDs) != 1000 {
		t.Fatalf("actual maximal result missing: %v", err)
	}
	return s, bundle, mapping, preview.Digest, requestID, result
}

func permanentReplayDigest(t *testing.T, s *Store) string {
	t.Helper()
	rows, err := s.db.Query(`SELECT request_id,request_digest,result_json,created_at FROM service_alert_imports ORDER BY request_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	hash := sha256.New()
	for rows.Next() {
		var id, digest, result, at string
		if err := rows.Scan(&id, &digest, &result, &at); err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{id, digest, result, at} {
			fmt.Fprintf(hash, "%d:", len(value))
			io.WriteString(hash, value)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func TestServiceAlertPermanentReplayCapacity(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_SERVICE_REPLAY_CAPACITY") != "1" {
		t.Skip("disk-backed permanent receipt capacity fixture is opt-in")
	}
	for _, idLength := range []int{32, 128} {
		t.Run(fmt.Sprintf("destination-id-%d", idLength), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			s, bundle, mapping, preview, requestID, first := serviceReplayCapacityFixture(t, idLength)
			fileSize := func(name string) int64 {
				info, err := os.Stat(name)
				if err != nil {
					return 0
				}
				return info.Size()
			}
			if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
				t.Fatal(err)
			}
			levels := []map[string]any{}
			filled := 1
			for _, count := range []int{1000, 10000} {
				started := time.Now()
				if _, err := s.db.ExecContext(ctx, `WITH RECURSIVE seq(i) AS (VALUES(?) UNION ALL SELECT i+1 FROM seq WHERE i+1<?)
                INSERT INTO service_alert_imports(request_id,request_digest,result_json,created_at)
                SELECT ?||printf('%08d',i),r.request_digest,r.result_json,r.created_at FROM seq CROSS JOIN service_alert_imports r WHERE r.request_id=?`, filled, count, strings.Repeat("r", 120), requestID); err != nil {
					t.Fatal(err)
				}
				seedElapsed := time.Since(started)
				filled = count
				walAllocated := fileSize(s.path + "-wal")
				if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
					t.Fatal(err)
				}
				var rows, totalJSON, resultBytes, pageSize, pages, freePages int64
				if err := s.db.QueryRow(`SELECT COUNT(*),SUM(length(result_json)),MAX(length(result_json)) FROM service_alert_imports`).Scan(&rows, &totalJSON, &resultBytes); err != nil {
					t.Fatal(err)
				}
				if rows != int64(count) {
					t.Fatal("permanent history did not reach intended capacity")
				}
				for _, item := range []struct {
					query  string
					target *int64
				}{{"PRAGMA page_size", &pageSize}, {"PRAGMA page_count", &pages}, {"PRAGMA freelist_count", &freePages}} {
					if err := s.db.QueryRow(item.query).Scan(item.target); err != nil {
						t.Fatal(err)
					}
				}
				timings := make([]float64, 0, 200)
				for i := 0; i < 200; i++ {
					key := strings.Repeat("r", 120) + fmt.Sprintf("%08d", i*count/200)
					started := time.Now()
					result, err := s.ApplyServiceAlertImport(ctx, key, bundle, mapping, preview)
					timings = append(timings, float64(time.Since(started))/float64(time.Millisecond))
					if err != nil || !result.Replayed || !reflect.DeepEqual(first.ServiceIDs, result.ServiceIDs) || result.Created != 1000 || result.Updated != 0 {
						t.Fatalf("full permanent result replay failed: %v", err)
					}
				}
				sort.Float64s(timings)
				levels = append(levels, map[string]any{"records": rows, "result_bytes": resultBytes, "total_result_bytes": totalJSON, "database_bytes": fileSize(s.path), "used_page_bytes": (pages - freePages) * pageSize, "seed_ms": float64(seedElapsed) / float64(time.Millisecond), "seed_wal_allocated_bytes": walAllocated, "replay_calls": len(timings), "replay_p50_ms": timings[len(timings)/2], "replay_p95_ms": timings[189], "replay_max_ms": timings[len(timings)-1]})
			}
			fingerprint := permanentReplayDigest(t, s)
			beforeSettings := rowCount(t, s, "http_service_alerts")
			if _, err := s.ApplyServiceAlertImport(ctx, strings.Repeat("r", 120)+"99999999", bundle, mapping, preview); !errors.Is(err, ErrServiceAlertImportLimit) {
				t.Fatalf("full ledger did not reject new identity: %v", err)
			}
			if rowCount(t, s, "http_service_alerts") != beforeSettings || permanentReplayDigest(t, s) != fingerprint {
				t.Fatal("rejected overflow changed permanent history/configuration")
			}
			if _, err := s.db.ExecContext(ctx, `DELETE FROM http_services`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `DELETE FROM notification_channels`); err != nil {
				t.Fatal(err)
			}
			if err := s.ApplyRetention(ctx, time.Now().UTC().Add(400*24*time.Hour), DefaultRetentionPolicy()); err != nil {
				t.Fatal(err)
			}
			result, err := s.ApplyServiceAlertImport(ctx, requestID, bundle, mapping, preview)
			if err != nil || !result.Replayed || !reflect.DeepEqual(result.ServiceIDs, first.ServiceIDs) || permanentReplayDigest(t, s) != fingerprint {
				t.Fatal("destination removal/retention changed committed replay")
			}
			backup := filepath.Join(t.TempDir(), "complete-replay-backup.db")
			started := time.Now()
			if err := s.ConsistentBackup(ctx, backup); err != nil {
				t.Fatal(err)
			}
			backupElapsed := time.Since(started)
			if err := ValidateDatabaseFile(ctx, backup); err != nil {
				t.Fatal(err)
			}
			restored, err := Open(ctx, backup)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			if permanentReplayDigest(t, restored) != fingerprint {
				t.Fatal("complete backup omitted or changed immutable records")
			}
			result, err = restored.ApplyServiceAlertImport(ctx, requestID, bundle, mapping, preview)
			if err != nil || !result.Replayed || !reflect.DeepEqual(first.ServiceIDs, result.ServiceIDs) || rowCount(t, restored, "service_alert_imports") != 10000 {
				t.Fatal("normal restored Store cannot replay full history")
			}
			evidence := map[string]any{"schema": "myprobe.service_replay_capacity.v1", "destination_id_length": idLength, "request_id_length": 128, "services_per_result": 1000, "record_limit": 10000, "levels": levels, "ledger_sha256": fingerprint, "backup_bytes": fileSize(backup), "backup_ms": float64(backupElapsed) / float64(time.Millisecond), "restored_records": 10000, "deleted_destinations_replayed": true, "retention_preserved_history": true, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "logical_cpus": runtime.NumCPU(), "recorded_at": time.Now().UTC()}
			encoded, err := json.Marshal(evidence)
			if err != nil {
				t.Fatal(err)
			}
			t.Log(string(encoded))
			if directory := os.Getenv("MYPROBE_TEST_SERVICE_REPLAY_OUTPUT"); directory != "" {
				if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("service-replay-%d.json", idLength)), append(encoded, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
