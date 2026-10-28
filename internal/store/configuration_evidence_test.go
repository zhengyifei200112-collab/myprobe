package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

func TestSamplingConfigurationRevisionEpochAndNonceFences(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: "synthetic config node"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	epoch := strings.Repeat("a", 32)
	first, err := db.BeginAgentConnection(ctx, node.ID, epoch, 2, true, at)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := db.SamplingConfigurationEvidence(ctx, node.ID)
	if err != nil || evidence.Status != "prepared" || evidence.AppliedRevision != nil {
		t.Fatalf("prepared is not applied: %+v %v", evidence, err)
	}
	if err = db.CompleteSamplingDispatch(ctx, node.ID, epoch, first.Nonce, true, at); err != nil {
		t.Fatal(err)
	}
	if err = db.AcknowledgeSamplingConfiguration(ctx, node.ID, epoch, v2.ConfigurationApplied(*first), at); err != nil {
		t.Fatal(err)
	}
	evidence, err = db.SamplingConfigurationEvidence(ctx, node.ID)
	if err != nil || evidence.Status != "agent_confirmed" || *evidence.AppliedRevision != 1 {
		t.Fatal("accepted application not recorded")
	}
	// Metadata-only changes do not create a false configuration revision.
	if _, err = db.db.ExecContext(ctx, `UPDATE nodes SET name='renamed' WHERE id=?`, node.ID); err != nil {
		t.Fatal(err)
	}
	desired, err := db.DesiredSamplingConfiguration(ctx, node.ID)
	if err != nil || desired.Revision != 1 {
		t.Fatal("metadata edit changed config identity")
	}
	if _, err = db.db.ExecContext(ctx, `UPDATE nodes SET report_seconds=9 WHERE id=?`, node.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.AcknowledgeSamplingConfiguration(ctx, node.ID, epoch, v2.ConfigurationApplied(*first), at.Add(time.Second)); !errors.Is(err, ErrConfigurationSuperseded) {
		t.Fatal("old revision acknowledgement accepted")
	}
	next, err := db.PrepareSamplingConfiguration(ctx, node.ID, epoch)
	if err != nil || next.Revision != 2 || next.Nonce == first.Nonce {
		t.Fatal("new revision/nonce missing")
	}
	incorrect := v2.ConfigurationApplied(next)
	incorrect.ReportSeconds = 8
	if err = db.AcknowledgeSamplingConfiguration(ctx, node.ID, epoch, incorrect, at.Add(time.Second)); !errors.Is(err, ErrConfigurationSuperseded) {
		t.Fatal("unapplied period accepted")
	}
	if err = db.AcknowledgeSamplingConfiguration(ctx, node.ID, epoch, v2.ConfigurationApplied(next), at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	evidence, err = db.SamplingConfigurationEvidence(ctx, node.ID)
	if err != nil || evidence.AcknowledgedAt == nil || !evidence.AcknowledgedAt.Equal(at.Add(2*time.Second)) {
		t.Fatal("new acknowledgement retained an old timestamp")
	}
	if err = db.AcknowledgeSamplingConfiguration(ctx, node.ID, epoch, v2.ConfigurationApplied(next), at.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	evidence, _ = db.SamplingConfigurationEvidence(ctx, node.ID)
	if !evidence.AcknowledgedAt.Equal(at.Add(2 * time.Second)) {
		t.Fatal("duplicate acknowledgement changed original receipt time")
	}
	newerEpoch := strings.Repeat("b", 32)
	latest, err := db.BeginAgentConnection(ctx, node.ID, newerEpoch, 2, true, at.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AcknowledgeSamplingConfiguration(ctx, node.ID, epoch, v2.ConfigurationApplied(next), at.Add(5*time.Second)); !errors.Is(err, ErrConfigurationSuperseded) {
		t.Fatal("replaced connection was authorized")
	}
	if err = db.AcknowledgeSamplingConfiguration(ctx, node.ID, newerEpoch, v2.ConfigurationApplied(*latest), at.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Returning to the original values is a new identity, not permission to reuse
	// the original acknowledgement. The trigger also catches non-UI writes.
	if _, err := db.db.ExecContext(ctx, `UPDATE nodes SET report_seconds=5 WHERE id=?`, node.ID); err != nil {
		t.Fatal(err)
	}
	desired, err = db.DesiredSamplingConfiguration(ctx, node.ID)
	if err != nil || desired.Revision != 3 {
		t.Fatal("A to B to A reused an old configuration revision")
	}
	if err := db.AcknowledgeSamplingConfiguration(ctx, node.ID, newerEpoch, v2.ConfigurationApplied(*first), at.Add(6*time.Second)); !errors.Is(err, ErrConfigurationSuperseded) {
		t.Fatal("returning settings authorized an old acknowledgement")
	}
	raw, _ := json.Marshal(evidence)
	if strings.Contains(string(raw), epoch) || strings.Contains(string(raw), next.Nonce) || strings.Contains(string(raw), "connection_epoch") {
		t.Fatal("diagnostics disclosed internal fences")
	}
}

func TestSamplingConfigurationLegacySupportIsUnknownNotApplied(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: "synthetic legacy node"})
	if err != nil {
		t.Fatal(err)
	}
	config, err := db.BeginAgentConnection(ctx, node.ID, strings.Repeat("c", 32), 1, false, time.Now())
	if err != nil || config != nil {
		t.Fatal("legacy connection received extension")
	}
	evidence, err := db.SamplingConfigurationEvidence(ctx, node.ID)
	if err != nil || evidence.Status != "acknowledgement_unsupported" || evidence.AppliedRevision != nil {
		t.Fatal("legacy welcome was claimed applied")
	}
}
