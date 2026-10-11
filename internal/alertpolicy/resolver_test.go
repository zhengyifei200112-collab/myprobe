package alertpolicy

import (
	"fmt"
	"reflect"
	"testing"
)

func TestResolverOwnsValidatedSelectors(t *testing.T) {
	policies := []Policy{
		{ID: "global", Key: "cpu", Enabled: true, Scope: Scope{Kind: "all"}},
		{ID: "tag", Key: "cpu", Enabled: true, Scope: Scope{Kind: "tags", Tags: []string{"prod"}, TagMode: "all"}},
		{ID: "explicit", Key: "cpu", Enabled: true, Scope: Scope{Kind: "nodes", NodeIDs: []string{"a"}}},
	}
	resolver, err := NewResolver(policies)
	if err != nil {
		t.Fatal(err)
	}
	nodes := []Node{{ID: "a", Tags: []string{"prod"}}, {ID: "b", Tags: []string{"prod"}}, {ID: "c"}}
	expected := make([][]Decision, len(nodes))
	for i, node := range nodes {
		expected[i], err = Resolve(policies, node)
		if err != nil {
			t.Fatal(err)
		}
	}
	policies[0].Enabled = false
	policies[1].Scope.Tags[0] = "changed"
	policies[2].Scope.NodeIDs[0] = "changed"
	for i, node := range nodes {
		actual, err := resolver.Resolve(node)
		if err != nil || !reflect.DeepEqual(actual, expected[i]) {
			t.Fatalf("snapshot changed for %s: %+v %v", node.ID, actual, err)
		}
		actual[0].CandidateIDs[0] = "caller mutation"
		again, err := resolver.Resolve(node)
		if err != nil || !reflect.DeepEqual(again, expected[i]) {
			t.Fatal("result aliases resolver state")
		}
	}
	if _, err := resolver.Resolve(Node{}); err == nil {
		t.Fatal("invalid observer accepted")
	}
	if _, err := NewResolver([]Policy{{ID: "invalid"}}); err == nil {
		t.Fatal("invalid snapshot accepted")
	}
}

func BenchmarkPolicyNodeSelection(b *testing.B) {
	policies := make([]Policy, 100)
	for i := range policies {
		policies[i] = Policy{ID: fmt.Sprintf("policy-%d", i), Key: fmt.Sprintf("key-%d", i), Enabled: true, Scope: Scope{Kind: "tags", Tags: []string{"prod"}, TagMode: "all"}}
	}
	nodes := make([]Node, 100)
	for i := range nodes {
		nodes[i] = Node{ID: fmt.Sprintf("node-%d", i), Tags: []string{"prod"}}
	}
	b.Run("validate-each-node", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for _, node := range nodes {
				if _, err := Resolve(policies, node); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	b.Run("snapshot-per-cycle", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			resolver, err := NewResolver(policies)
			if err != nil {
				b.Fatal(err)
			}
			for _, node := range nodes {
				if _, err := resolver.Resolve(node); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}
