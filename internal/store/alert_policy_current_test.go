package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
)

func TestPolicyChangesInvalidateObservationsAndDeliveriesBeforeSync(t *testing.T) {
	for _, change := range []string{"disable", "threshold", "scope", "override", "tags", "rename"} {
		for _, leased := range []bool{false, true} {
			t.Run(change+map[bool]string{false: "/pending", true: "/leased"}[leased], func(t *testing.T) {
				s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
				ctx := context.Background()
				at := time.Now().UTC().Truncate(time.Second)
				if _, err := s.PrepareAlertPolicyRules(ctx, at); err != nil {
					t.Fatal(err)
				}
				items, err := s.ListAlertPolicies(ctx)
				if err != nil || len(items) != 1 {
					t.Fatalf("policies: %+v %v", items, err)
				}
				p := items[0]
				if change == "tags" {
					p.Scope = alertpolicy.Scope{Kind: "tags", Tags: []string{"prod"}, TagMode: "all"}
					p, err = s.SaveAlertPolicy(ctx, p)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = s.db.ExecContext(ctx, `UPDATE nodes SET tags_json='["prod"]' WHERE id=?`, node.ID); err != nil {
						t.Fatal(err)
					}
				}
				observe(t, s, rule, node, at, true, true)
				var job *NotificationDelivery
				if leased {
					job, err = s.ClaimDelivery(ctx, at, time.Minute)
					if err != nil || job == nil {
						t.Fatalf("initial claim: %+v %v", job, err)
					}
				}
				switch change {
				case "disable":
					p.Enabled = false
				case "threshold":
					p.Config = json.RawMessage(`{"threshold_percent":95}`)
				case "scope":
					p.Scope = alertpolicy.Scope{Kind: "tags", Tags: []string{"absent"}, TagMode: "all"}
				case "override":
					p.ID, p.Revision, p.Priority = "", 0, 1
				case "rename":
					p.Name = "cosmetic change"
				case "tags":
					if _, err = s.db.ExecContext(ctx, `UPDATE nodes SET tags_json='[]' WHERE id=?`, node.ID); err != nil {
						t.Fatal(err)
					}
				}
				if change != "tags" {
					if _, err = s.SaveAlertPolicy(ctx, p); err != nil {
						t.Fatal(err)
					}
				}
				// No synchronization: the materialized rule still has its old values.
				_, err = s.ObserveAlert(ctx, rule, node, AlertObservation{At: at.Add(time.Second), Known: true, Active: true})
				if change == "rename" {
					if err != nil {
						t.Fatal("cosmetic edit invalidated observation", err)
					}
				} else if !errors.Is(err, ErrObservationObsolete) {
					t.Fatalf("obsolete observation accepted: %v", err)
				}
				if leased {
					err = s.CheckDeliveryLease(ctx, job.ID, job.LeaseToken, at.Add(2*time.Second))
					if change == "rename" && err != nil || change != "rename" && !errors.Is(err, ErrDeliveryCanceled) {
						t.Fatalf("lease validation: %v", err)
					}
				} else {
					job, err = s.ClaimDelivery(ctx, at.Add(2*time.Second), time.Minute)
					if err != nil || (job != nil) != (change == "rename") {
						t.Fatalf("claim after edit: %+v %v", job, err)
					}
				}
			})
		}
	}
}
