package networkcheck

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

var fixtureTime = time.Date(2026, 10, 15, 16, 0, 0, 0, time.UTC)

func fixtureTask() Task {
	return Task{Task: v1.Task{ID: "synthetic-task", TargetID: "synthetic-target", Kind: v1.TaskKindPing, Host: "example.test", TimeoutMS: 3000, ExpiresAt: fixtureTime.Add(time.Minute)}, AddressFamily: FamilyIPv6, PacketCount: 5}
}
func fixtureResult() Result {
	rtt := 12.5
	return Result{TaskID: "synthetic-task", TargetID: "synthetic-target", Kind: v1.TaskKindPing, Outcome: Success, AddressFamily: FamilyIPv6, RTTMS: &rtt, Packets: &PacketCounts{Sent: 5, Received: 3}, CompletedAt: fixtureTime}
}

func TestTaskBoundsFamilyAndTotalBudget(t *testing.T) {
	if err := fixtureTask().Validate(fixtureTime); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Task){
		"missing family":                     func(t *Task) { t.AddressFamily = "" },
		"unknown request family":             func(t *Task) { t.AddressFamily = FamilyUnknown },
		"family spelling":                    func(t *Task) { t.AddressFamily = "IPv6" },
		"zero packets":                       func(t *Task) { t.PacketCount = 0 },
		"too many packets":                   func(t *Task) { t.PacketCount = 6 },
		"packet train exceeds total timeout": func(t *Task) { t.TimeoutMS = 1000 },
		"negative packets":                   func(t *Task) { t.PacketCount = -1 },
		"invalid task ID":                    func(t *Task) { t.ID = "task\x00" },
		"host whitespace":                    func(t *Task) { t.Host = " example.test" },
		"command flag":                       func(t *Task) { t.Host = "-f" },
		"literal family mismatch":            func(t *Task) { t.Host = "192.0.2.53" },
		"mapped IPv4 is not IPv6":            func(t *Task) { t.Host = "::ffff:192.0.2.53" },
		"ping has port":                      func(t *Task) { t.Port = 443 },
		"TCP has packet count":               func(t *Task) { t.Kind = v1.TaskKindTCPing; t.Port = 443 },
		"expired":                            func(t *Task) { t.ExpiresAt = fixtureTime.Add(-time.Nanosecond) },
		"expiry too far":                     func(t *Task) { t.ExpiresAt = fixtureTime.Add(11 * time.Minute) },
	} {
		t.Run(name, func(t *testing.T) {
			task := fixtureTask()
			mutate(&task)
			if task.Validate(fixtureTime) == nil {
				t.Fatal("invalid task accepted")
			}
		})
	}
	tcp := fixtureTask()
	tcp.Kind = v1.TaskKindTCPing
	tcp.Port = 443
	tcp.PacketCount = 0
	if tcp.Validate(fixtureTime) != nil {
		t.Fatal("valid TCP request rejected")
	}
	auto := fixtureTask()
	auto.AddressFamily = FamilyAuto
	auto.Host = "192.0.2.53"
	if auto.Validate(fixtureTime) != nil {
		t.Fatal("auto literal rejected")
	}
}

func TestResultProofAndPopulationBinding(t *testing.T) {
	if fixtureResult().ValidateAgainst(fixtureTask(), fixtureTime) != nil {
		t.Fatal("partial packet reception is a valid success with separate loss counts")
	}
	for name, mutate := range map[string]func(*Result){
		"wrong task":                  func(r *Result) { r.TaskID = "other" },
		"wrong target":                func(r *Result) { r.TargetID = "other" },
		"wrong family":                func(r *Result) { r.AddressFamily = FamilyIPv4 },
		"unknown successful family":   func(r *Result) { r.AddressFamily = FamilyUnknown },
		"missing RTT":                 func(r *Result) { r.RTTMS = nil },
		"NaN":                         func(r *Result) { value := math.NaN(); r.RTTMS = &value },
		"infinite RTT":                func(r *Result) { value := math.Inf(1); r.RTTMS = &value },
		"RTT out of range":            func(r *Result) { value := 60001.0; r.RTTMS = &value },
		"negative RTT":                func(r *Result) { value := -1.0; r.RTTMS = &value },
		"received exceeds sent":       func(r *Result) { r.Packets.Received = 6 },
		"negative count":              func(r *Result) { r.Packets.Sent = -1 },
		"no reply success":            func(r *Result) { r.Packets.Received = 0 },
		"success with error":          func(r *Result) { r.ErrorClass = "timeout" },
		"unsupported is not failure":  func(r *Result) { r.Outcome = Failure; r.RTTMS = nil; r.Packets = nil; r.ErrorClass = "unsupported" },
		"DNS is not executed failure": func(r *Result) { r.Outcome = Failure; r.RTTMS = nil; r.Packets = nil; r.ErrorClass = "dns" },
		"unsolicited error text":      func(r *Result) { r.ErrorClass = "192.0.2.53:443 refused" },
		"control ID":                  func(r *Result) { r.TaskID = "task\n" },
		"unknown outcome":             func(r *Result) { r.Outcome = "unknown" },
		"future":                      func(r *Result) { r.CompletedAt = fixtureTime.Add(2 * time.Minute) },
		"stale":                       func(r *Result) { r.CompletedAt = fixtureTime.Add(-11 * time.Minute) },
	} {
		t.Run(name, func(t *testing.T) {
			result := fixtureResult()
			mutate(&result)
			if result.ValidateAgainst(fixtureTask(), fixtureTime) == nil {
				t.Fatal("invalid/binding-mismatched result accepted")
			}
		})
	}
	task := fixtureTask()
	task.PacketCount = 2
	if fixtureResult().ValidateAgainst(task, fixtureTime) == nil {
		t.Fatal("sent count exceeded durable task request")
	}
	failed := fixtureResult()
	failed.Outcome = Failure
	failed.ErrorClass = "timeout"
	failed.RTTMS = nil
	failed.Packets.Received = 0
	if failed.ValidateAgainst(fixtureTask(), fixtureTime) != nil {
		t.Fatal("settled total packet loss rejected")
	}
	unknown := fixtureResult()
	unknown.Outcome = Unobserved
	unknown.ErrorClass = "unsupported"
	unknown.AddressFamily = FamilyUnknown
	unknown.RTTMS = nil
	unknown.Packets = nil
	if unknown.ValidateAgainst(fixtureTask(), fixtureTime) != nil {
		t.Fatal("unsupported must be representable without invented packets/family")
	}
	unknown.Packets = &PacketCounts{Sent: 0, Received: 0}
	if unknown.Validate(fixtureTime) == nil {
		t.Fatal("unobserved claimed settled packet evidence")
	}
	tcp := fixtureResult()
	tcp.Kind = v1.TaskKindTCPing
	tcp.Packets = nil
	if tcp.Validate(fixtureTime) != nil {
		t.Fatal("TCP success without packet counts rejected")
	}
	tcp.Packets = &PacketCounts{Sent: 1, Received: 1}
	if tcp.Validate(fixtureTime) == nil {
		t.Fatal("TCP claimed ICMP packet counts")
	}
}

func TestStrictWireDecodingAndExplicitCounts(t *testing.T) {
	taskRaw, _ := json.Marshal(fixtureTask())
	if _, err := DecodeTask(taskRaw, fixtureTime); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(fixtureResult())
	if _, err := DecodeResult(raw, fixtureTime); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		string(raw) + `{}`, strings.Replace(string(raw), `"received":3`, `"received":1,"host":"192.0.2.53"`, 1),
		strings.Replace(string(raw), `"sent":5,`, ``, 1), strings.Replace(string(raw), `,"received":3`, ``, 1),
		strings.Replace(string(raw), `"sent":5`, `"sent":1.5`, 1), strings.Replace(string(raw), `"sent":5`, `"sent":"5"`, 1),
		`null`, `[]`, ``, strings.Repeat(" ", v1.MaxMessageBytes+1),
	} {
		if _, err := DecodeResult([]byte(invalid), fixtureTime); err == nil {
			t.Fatalf("invalid wire result accepted: %.200s", invalid)
		}
	}
	if _, err := DecodeTask(append(taskRaw, []byte(`{}`)...), fixtureTime); err == nil {
		t.Fatal("multiple task objects accepted")
	}
	// A task can expire before receipt; task identity and receive/lease grace are
	// checked by the ledger, not by pretending it is a newly dispatchable task.
	past := fixtureTask()
	past.ExpiresAt = fixtureTime.Add(-time.Second)
	if fixtureResult().ValidateAgainst(past, fixtureTime) != nil {
		t.Fatal("definition validation reset dispatched task expiry")
	}
}
