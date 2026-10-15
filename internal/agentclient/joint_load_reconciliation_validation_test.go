package agentclient

import (
	"crypto/sha256"
	"testing"
	"time"
)

func TestReportReconciliationRequiresACKStorageAndExplainedInterruptions(t *testing.T) {
	now := time.Now()
	hash := sha256.Sum256([]byte("synthetic report"))
	for _, tc := range []struct {
		name                             string
		acknowledged, stored             bool
		interruptedAfter                 time.Duration
		missing, ackMissing, interrupted int
	}{
		{"complete", true, true, 0, 0, 0, 0},
		{"lost-ack-stored", false, true, time.Second, 0, 0, 0},
		{"ack-missing-storage", true, false, time.Second, 0, 1, 0},
		{"transport-interruption", false, false, time.Second, 0, 0, 1},
		{"silent-loss-not-excused-by-later-close", false, false, time.Minute, 1, 0, 0},
		{"silent-loss-with-live-connection", false, false, 0, 1, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempt := &jointReportAttempt{node: "synthetic-node", sequence: 2, captured: "synthetic-time", hash: hash, sentAt: now, forwarded: true, acknowledged: tc.acknowledged}
			if tc.interruptedAfter > 0 {
				attempt.interruptedAt = now.Add(tc.interruptedAfter)
			}
			observer := &jointReportObserver{attempts: map[jointAttemptKey]*jointReportAttempt{{"synthetic-node", 2}: attempt}}
			stored := map[string]map[string][32]byte{}
			if tc.stored {
				stored["synthetic-node"] = map[string][32]byte{"synthetic-time": hash}
			}
			result := observer.reconcile(stored, 0)
			if result.UnexplainedNotStored != tc.missing || result.ACKWithoutStored != tc.ackMissing || result.InterruptedNotStored != tc.interrupted {
				t.Fatalf("misclassified receipt/storage state: %+v", result)
			}
		})
	}
}

func TestReportReconciliationDetectsUnknownChangedAndDuplicateStoredData(t *testing.T) {
	hash := sha256.Sum256([]byte("original"))
	other := sha256.Sum256([]byte("changed"))
	observer := &jointReportObserver{attempts: map[jointAttemptKey]*jointReportAttempt{{"node", 2}: {node: "node", captured: "known", hash: hash, sentAt: time.Now(), acknowledged: true}}}
	result := observer.reconcile(map[string]map[string][32]byte{"node": {"known": other, "unknown": hash}}, 1)
	if result.UnknownStored != 1 || result.HashMismatch != 1 || result.DuplicateStored != 1 {
		t.Fatalf("storage corruption not detected: %+v", result)
	}
}

func TestReportObserverACKCannotConfirmAnotherNodeOrSequence(t *testing.T) {
	attempt := &jointReportAttempt{node: "node", sequence: 2, sentAt: time.Now()}
	observer := &jointReportObserver{attempts: map[jointAttemptKey]*jointReportAttempt{{"node", 2}: attempt}}
	observer.ack("other-node", 2)
	observer.ack("node", 3)
	if attempt.acknowledged {
		t.Fatal("unrelated receipt confirmed this report")
	}
	observer.ack("node", 2)
	first := attempt.ackAt
	observer.ack("node", 2)
	if !attempt.acknowledged || first.IsZero() || !attempt.ackAt.Equal(first) {
		t.Fatal("matching repeated receipt changed first proof")
	}
}

func TestReportObserverSequenceGapCannotDisappearBeforeProxy(t *testing.T) {
	observer := &jointReportObserver{sequences: map[string]map[uint64]bool{"node": {1: true, 3: true}}}
	var result jointReportAccounting
	observer.sequenceAccounting(&result, map[string]uint64{"node": 3})
	if result.GeneratedEnvelopes != 3 || result.ObservedEnvelopes != 2 || result.UnobservedGeneratedEnvelopes != 1 {
		t.Fatalf("pre-observer gap hidden: %+v", result)
	}
}
