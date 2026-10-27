package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestServiceRecreationFencesOldIncidentAndDelivery(t *testing.T) {
	for _, stage := range []string{"observation", "pending", "leased", "authorized", "reconcile"} {
		t.Run(stage, func(t *testing.T) {
			s, service, channel, node, settings := serviceAlertFixture(t)
			ctx := context.Background()
			at := time.Now().UTC().Add(time.Second)
			if _, err := s.PrepareAlertPolicyRules(ctx, at); err != nil {
				t.Fatal(err)
			}
			rules, err := s.ListAlertRules(ctx)
			if err != nil || len(rules) != 1 {
				t.Fatal("missing rule", err)
			}
			old := rules[0]
			incident := observe(t, s, old, node, at, true, true)
			var delivery *NotificationDelivery
			if stage == "leased" || stage == "authorized" {
				delivery, err = s.ClaimDelivery(ctx, at, time.Minute)
				if err != nil || delivery == nil {
					t.Fatal("missing delivery", err)
				}
				if stage == "authorized" {
					if err := s.CheckDeliveryLease(ctx, delivery.ID, delivery.LeaseToken, at, channel.DeliveryRevision); err != nil {
						t.Fatal(err)
					}
				}
			}
			snapshot, err := s.ExportConfig(ctx, at)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteHTTPService(ctx, service.ID, service.Revision); err != nil {
				t.Fatal(err)
			}
			result, err := s.ImportConfig(ctx, snapshot, false)
			if err != nil || result.HTTPServicesCreated != 1 {
				t.Fatal("recreation failed", err)
			}
			recreated, err := s.HTTPService(ctx, service.ID)
			if err != nil || recreated.Revision != service.Revision || recreated.UpdatedAt.Equal(service.UpdatedAt) {
				t.Fatal("fixture must reuse revision with a new epoch", err)
			}
			settings.Revision = 0
			current, err := s.SaveServiceAlertSettings(ctx, settings)
			if err != nil || current.Revision != 1 {
				t.Fatal("settings did not reuse revision", err)
			}
			switch stage {
			case "observation":
				if _, err := s.ObserveAlert(ctx, old, node, AlertObservation{At: at.Add(time.Second), Known: true, Active: true}); !errors.Is(err, ErrObservationObsolete) {
					t.Fatal("old observation retained authority", err)
				}
			case "pending":
				if d, err := s.ClaimDelivery(ctx, at.Add(time.Second), time.Minute); err != nil || d != nil {
					t.Fatal("old pending delivery claimed", err)
				}
			case "leased":
				if err := s.CheckDeliveryLease(ctx, delivery.ID, delivery.LeaseToken, at.Add(time.Second), channel.DeliveryRevision); !errors.Is(err, ErrDeliveryCanceled) {
					t.Fatal("old lease retained send authority", err)
				}
			case "authorized":
				if err := s.CompleteDelivery(ctx, delivery.ID, delivery.LeaseToken, at.Add(time.Second), DeliveryOutcome{Delivered: true, ChannelRevision: channel.DeliveryRevision}); err != nil {
					t.Fatal(err)
				}
				job, err := s.Delivery(ctx, delivery.ID)
				if err != nil || job.Status != "canceled" || job.ErrorClass != "configuration_changed" {
					t.Fatal("old receipt retained authority", err)
				}
				attempts, err := s.ListDeliveryAttempts(ctx, delivery.ID)
				if err != nil || len(attempts) != 1 || attempts[0].Outcome != "delivered" {
					t.Fatal("actual receipt lost", err)
				}
			}
			if _, err := s.PrepareAlertPolicyRules(ctx, at.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := s.ReconcileIncidents(ctx, at.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			ended, err := s.Incident(ctx, incident.ID)
			if err != nil || ended.State != "resolved" || ended.ResolutionReason != "rule_changed" {
				t.Fatal("old incident survived recreation", err)
			}
			fresh, err := s.ListAlertRules(ctx)
			if err != nil || len(fresh) != 1 || string(fresh[0].Config) == string(old.Config) {
				t.Fatal("rule snapshot did not advance", err)
			}
			if _, err := s.ObserveAlert(ctx, fresh[0], node, AlertObservation{At: at.Add(3 * time.Second), Known: false}); err != nil {
				t.Fatal(err)
			}
			if rowCount(t, s, "notification_deliveries") != 1 {
				t.Fatal("recreation manufactured a notification")
			}
		})
	}
}

func TestServiceLegacySnapshotIsReconciledWithoutRecovery(t *testing.T) {
	s, _, _, node, _ := serviceAlertFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Second)
	if _, err := s.PrepareAlertPolicyRules(ctx, at); err != nil {
		t.Fatal(err)
	}
	rules, err := s.ListAlertRules(ctx)
	if err != nil || len(rules) != 1 {
		t.Fatal(err)
	}
	old := observe(t, s, rules[0], node, at, true, true)
	var legacy map[string]any
	if err := json.Unmarshal(rules[0].Config, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "service_epoch")
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE alert_rules SET config_json=? WHERE id=?`, string(raw), rules[0].ID); err != nil {
		t.Fatal(err)
	}
	// Model an incident and pending intent from a prior server, without authorizing
	// the legacy rule through the new observation guard.
	rules[0].Config = raw
	if _, err := s.db.Exec(`UPDATE alert_incidents SET rule_snapshot_json=? WHERE id=?`, string(snapshotRule(rules[0])), old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareAlertPolicyRules(ctx, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileIncidents(ctx, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	ended, err := s.Incident(ctx, old.ID)
	if err != nil || ended.State != "resolved" || ended.ResolutionReason != "rule_changed" || rowCount(t, s, "notification_deliveries") != 1 {
		t.Fatal("legacy incident was retained or recovered", err)
	}
}
