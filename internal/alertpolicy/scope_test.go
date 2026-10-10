package alertpolicy

import (
	"reflect"
	"testing"
)

func TestResolveInheritanceAndIndependentKeys(t *testing.T) {
	policies := []Policy{
		{ID: "global", Key: "cpu.warning", Enabled: true, Priority: 1000, Scope: Scope{Kind: "all"}},
		{ID: "tag", Key: "cpu.warning", Enabled: true, Priority: 10, Scope: Scope{Kind: "tags", TagMode: "all", Tags: []string{"prod", "linux"}}},
		{ID: "explicit", Key: "cpu.warning", Enabled: true, Priority: -1000, Scope: Scope{Kind: "nodes", NodeIDs: []string{"a"}}},
		{ID: "critical", Key: "cpu.critical", Enabled: true, Scope: Scope{Kind: "all"}},
	}
	for _, tc := range []struct {
		node   Node
		winner string
	}{
		{Node{ID: "a", Tags: []string{"prod", "linux"}}, "explicit"},
		{Node{ID: "new", Tags: []string{"prod", "linux"}}, "tag"},
		{Node{ID: "partial", Tags: []string{"prod"}}, "global"},
	} {
		decisions, err := Resolve(policies, tc.node)
		if err != nil || len(decisions) != 2 {
			t.Fatalf("decisions=%+v error=%v", decisions, err)
		}
		if decisions[0].SelectedID != "critical" || decisions[1].SelectedID != tc.winner {
			t.Fatalf("unexpected precedence: %+v", decisions)
		}
	}
	d, _ := Resolve(policies, Node{ID: "a", Tags: []string{"prod", "linux"}})
	if !reflect.DeepEqual(d[1].CandidateIDs, []string{"explicit", "tag", "global"}) || d[1].Reason != "more_specific_scope" {
		t.Fatal(d)
	}
	policies[2].Enabled = false
	d, _ = Resolve(policies, Node{ID: "a", Tags: []string{"prod", "linux"}})
	if d[1].SelectedID != "tag" {
		t.Fatal("disabled policy still overrides")
	}
}

func TestConflictIncludesFutureTagCombinations(t *testing.T) {
	a := Policy{ID: "a", Key: "disk", Enabled: true, Scope: Scope{Kind: "tags", TagMode: "all", Tags: []string{"east"}}}
	b := Policy{ID: "b", Key: "disk", Enabled: true, Scope: Scope{Kind: "tags", TagMode: "any", Tags: []string{"west"}}}
	if ValidateSet([]Policy{a, b}) == nil {
		t.Fatal("future node can have east and west tags")
	}
	b.Priority = 1
	decisions, err := Resolve([]Policy{a, b}, Node{ID: "new", Tags: []string{"east", "west"}})
	if err != nil || len(decisions) != 1 || decisions[0].SelectedID != "b" || decisions[0].Reason != "higher_priority" {
		t.Fatalf("%+v %v", decisions, err)
	}
	a.Scope = Scope{Kind: "nodes", NodeIDs: []string{"one"}}
	b.Scope = Scope{Kind: "nodes", NodeIDs: []string{"two"}}
	b.Priority = 0
	if err := ValidateSet([]Policy{a, b}); err != nil {
		t.Fatal("disjoint explicit scopes", err)
	}
	b.Scope.NodeIDs = append(b.Scope.NodeIDs, "one")
	if ValidateSet([]Policy{a, b}) == nil {
		t.Fatal("overlapping explicit scopes accepted")
	}
	b.Enabled = false
	if err := ValidateSet([]Policy{a, b}); err != nil {
		t.Fatal("disabled drafts must not conflict", err)
	}
}

func TestScopeRejectsAmbiguousOrUnboundedInputs(t *testing.T) {
	for _, scope := range []Scope{
		{}, {Kind: "all", Tags: []string{"x"}}, {Kind: "nodes"},
		{Kind: "nodes", NodeIDs: []string{"x", "x"}},
		{Kind: "tags", Tags: []string{"x"}},
		{Kind: "tags", TagMode: "any", Tags: []string{" x"}},
		{Kind: "tags", TagMode: "all", Tags: []string{"x\n"}},
		{Kind: "nodes", NodeIDs: make([]string, 101)},
	} {
		if scope.Validate() == nil {
			t.Fatalf("invalid scope accepted: %+v", scope)
		}
	}
	if _, err := Resolve([]Policy{{ID: "x", Key: "bad key", Scope: Scope{Kind: "all"}}}, Node{ID: "n"}); err == nil {
		t.Fatal("invalid key accepted")
	}
	if _, err := Resolve(nil, Node{}); err == nil {
		t.Fatal("empty node accepted")
	}
}

func TestResolveDeterministicAndDoesNotMutateInput(t *testing.T) {
	a := Policy{ID: "a", Key: "a", Enabled: true, Scope: Scope{Kind: "all"}}
	b := Policy{ID: "b", Key: "b", Enabled: true, Scope: Scope{Kind: "all"}}
	input := []Policy{b, a}
	left, err := Resolve(input, Node{ID: "n"})
	if err != nil {
		t.Fatal(err)
	}
	right, _ := Resolve([]Policy{a, b}, Node{ID: "n"})
	if !reflect.DeepEqual(left, right) || input[0].ID != "b" {
		t.Fatal("unstable result or mutated input")
	}
}
