package alerts

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/backup"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestEncryptedPolicyRestoreDeliversWithOriginalChannelKey(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "source.db")
	db, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, passphrase, credential := rand.Text()+rand.Text(), rand.Text(), rand.Text()
	messages := make(chan Notification, 4)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+credential {
			t.Error("restored channel credential mismatch")
			w.WriteHeader(401)
			return
		}
		var message Notification
		if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		messages <- message
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	s := New(db, key, NewHTTPSender(receiver.Client()), nil)
	channel, err := s.CreateChannel(ctx, "restore fixture", "webhook", ChannelConfig{URL: receiver.URL, Headers: map[string]string{"Authorization": "Bearer " + credential}})
	if err != nil {
		t.Fatal(err)
	}
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "restore fixture"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	expiry := now.Add(time.Hour)
	updateNodeExpiry(t, db, node, &expiry)
	policy, err := s.SavePolicy(ctx, store.AlertPolicy{Policy: alertpolicy.Policy{Key: "expiry", Enabled: true, Scope: alertpolicy.Scope{Kind: "all"}}, Name: "restore fixture", ChannelID: channel.ID, Kind: "expiry", Config: json.RawMessage(`{"days_before":1,"recovery_seconds":0}`), CooldownSeconds: 900})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	incidents, err := db.ListIncidents(ctx, store.IncidentFilter{State: "firing", Limit: 10})
	if err != nil || len(incidents) != 1 {
		t.Fatalf("source incidents: %+v %v", incidents, err)
	}
	jobs, err := db.ListIncidentDeliveries(ctx, incidents[0].ID, 0, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("source jobs: %+v %v", jobs, err)
	}
	snapshotPath := filepath.Join(directory, "snapshot.db")
	if err = db.ConsistentBackup(ctx, snapshotPath); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(credential)) {
		t.Fatal("channel credential stored in plaintext")
	}
	var archive bytes.Buffer
	if err = backup.Encrypt(&archive, bytes.NewReader(raw), passphrase); err != nil {
		t.Fatal(err)
	}
	var rejected bytes.Buffer
	if err = backup.Decrypt(&rejected, bytes.NewReader(archive.Bytes()), rand.Text()); err == nil {
		t.Fatal("wrong archive passphrase accepted")
	}
	var decoded bytes.Buffer
	if err = backup.Decrypt(&decoded, bytes.NewReader(archive.Bytes()), passphrase); err != nil {
		t.Fatal(err)
	}
	decryptedPath := filepath.Join(directory, "decrypted.db")
	// Restore into another destination, using the actual staged restore path.
	for _, correctKey := range []bool{true, false} {
		name := "original-key"
		restoreKey := key
		if !correctKey {
			name = "different-key"
			restoreKey = rand.Text() + rand.Text()
		}
		t.Run(name, func(t *testing.T) {
			// Staging takes ownership of the decrypted file; recreate each fixture.
			if err := os.WriteFile(decryptedPath, decoded.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "restored.db")
			if _, err := store.StageDatabaseRestore(ctx, destination, decryptedPath); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ApplyPendingRestore(ctx, destination, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			restored, err := store.Open(ctx, destination)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			runtime := New(restored, restoreKey, NewHTTPSender(receiver.Client()), nil)
			if err := runtime.Tick(ctx, now.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			policies, err := restored.ListAlertPolicies(ctx)
			if err != nil || len(policies) != 1 || policies[0].ID != policy.ID {
				t.Fatalf("restored policies: %+v %v", policies, err)
			}
			if worked, err := runtime.DeliverOne(ctx, now.Add(3*time.Second)); err != nil || !worked {
				t.Fatalf("restore delivery: %v %v", worked, err)
			}
			current, err := restored.ListIncidentDeliveries(ctx, incidents[0].ID, 0, 10)
			if err != nil || len(current) != 1 || current[0].ID != jobs[0].ID {
				t.Fatalf("restore duplicated job: %+v %v", current, err)
			}
			if correctKey {
				if current[0].Status != "delivered" {
					t.Fatalf("not delivered: %+v", current[0])
				}
				select {
				case message := <-messages:
					if message.RuleID != incidents[0].RuleID || message.State != "firing" {
						t.Fatal("notification identity changed")
					}
				default:
					t.Fatal("no actual restored notification")
				}
			} else {
				if current[0].ErrorClass != "channel_configuration" {
					t.Fatalf("wrong key not classified: %+v", current[0])
				}
				select {
				case <-messages:
					t.Fatal("wrong channel key sent a notification")
				default:
				}
			}
		})
	}
}
