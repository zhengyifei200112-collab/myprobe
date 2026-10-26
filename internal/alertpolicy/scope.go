// Package alertpolicy resolves scoped alert policies independently of delivery.
package alertpolicy

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Scope struct {
	Kind    string   `json:"kind"`
	NodeIDs []string `json:"node_ids,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	TagMode string   `json:"tag_mode,omitempty"`
}

type Policy struct {
	ID       string `json:"id"`
	Key      string `json:"policy_key"`
	Enabled  bool   `json:"enabled"`
	Priority int    `json:"priority"`
	Scope    Scope  `json:"scope"`
}

type Node struct {
	ID   string
	Tags []string
}

// Decision includes every matching candidate for an administrator's explanation.
// Candidates are ordered by specificity, then descending priority.
type Decision struct {
	Key          string   `json:"policy_key"`
	SelectedID   string   `json:"selected_id"`
	CandidateIDs []string `json:"candidate_ids"`
	Reason       string   `json:"reason"`
}

var keyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$`)

func validValue(v string, max int) bool {
	return v != "" && len(v) <= max && utf8.ValidString(v) && strings.TrimSpace(v) == v && strings.IndexFunc(v, unicode.IsControl) < 0
}

func uniqueValues(values []string, maxCount, maxLength int) bool {
	if len(values) == 0 || len(values) > maxCount {
		return false
	}
	seen := make(map[string]bool, len(values))
	for _, v := range values {
		if !validValue(v, maxLength) || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}

func (s Scope) Validate() error {
	switch s.Kind {
	case "all":
		if len(s.NodeIDs) == 0 && len(s.Tags) == 0 && s.TagMode == "" {
			return nil
		}
	case "nodes":
		if uniqueValues(s.NodeIDs, 100, 128) && len(s.Tags) == 0 && s.TagMode == "" {
			return nil
		}
	case "tags":
		if uniqueValues(s.Tags, 64, 128) && len(s.NodeIDs) == 0 && (s.TagMode == "all" || s.TagMode == "any") {
			return nil
		}
	}
	return errors.New("invalid alert policy scope")
}

func (s Scope) rank() int {
	switch s.Kind {
	case "nodes":
		return 3
	case "tags":
		return 2
	case "all":
		return 1
	}
	return 0
}

func (s Scope) matches(n Node) bool {
	switch s.Kind {
	case "all":
		return true
	case "nodes":
		for _, id := range s.NodeIDs {
			if id == n.ID {
				return true
			}
		}
	case "tags":
		tags := make(map[string]bool, len(n.Tags))
		for _, tag := range n.Tags {
			tags[tag] = true
		}
		for _, tag := range s.Tags {
			if s.TagMode == "any" && tags[tag] {
				return true
			}
			if s.TagMode == "all" && !tags[tag] {
				return false
			}
		}
		return s.TagMode == "all"
	}
	return false
}

// ValidateSet rejects ties before saving, including possible future tag overlap.
// With positive tag selectors, a future node can satisfy both selectors by having
// their union of tags. Checking only today's nodes would permit latent conflicts.
func ValidateSet(policies []Policy) error {
	if len(policies) > 1000 {
		return errors.New("too many alert policies")
	}
	seen := make(map[string]bool, len(policies))
	for _, p := range policies {
		if !validValue(p.ID, 128) || seen[p.ID] || !keyPattern.MatchString(p.Key) || p.Priority < -1000 || p.Priority > 1000 || p.Scope.Validate() != nil {
			return errors.New("invalid alert policy")
		}
		seen[p.ID] = true
	}
	type tier struct {
		key            string
		rank, priority int
	}
	owners := make(map[tier]map[string]string)
	for _, a := range policies {
		if !a.Enabled {
			continue
		}
		key := tier{a.Key, a.Scope.rank(), a.Priority}
		if owners[key] == nil {
			owners[key] = make(map[string]string)
		}
		ids := a.Scope.NodeIDs
		if a.Scope.Kind != "nodes" {
			ids = []string{""}
		}
		for _, id := range ids {
			if previous, ok := owners[key][id]; ok {
				return fmt.Errorf("ambiguous alert policies %q and %q", previous, a.ID)
			}
			owners[key][id] = a.ID
		}
	}
	return nil
}

func Resolve(policies []Policy, node Node) ([]Decision, error) {
	if err := ValidateSet(policies); err != nil {
		return nil, err
	}
	return (&Resolver{policies: policies}).Resolve(node)
}

// Resolver owns a validated snapshot, reusable across nodes in one database
// snapshot. Construct a new resolver after configuration changes.
type Resolver struct{ policies []Policy }

func NewResolver(policies []Policy) (*Resolver, error) {
	if err := ValidateSet(policies); err != nil {
		return nil, err
	}
	owned := append([]Policy(nil), policies...)
	for i := range owned {
		owned[i].Scope.NodeIDs = append([]string(nil), policies[i].Scope.NodeIDs...)
		owned[i].Scope.Tags = append([]string(nil), policies[i].Scope.Tags...)
	}
	return &Resolver{policies: owned}, nil
}

func (r *Resolver) Resolve(node Node) ([]Decision, error) {
	if !validValue(node.ID, 128) {
		return nil, errors.New("invalid policy observer")
	}
	groups := make(map[string][]Policy)
	for _, p := range r.policies {
		if p.Enabled && p.Scope.matches(node) {
			groups[p.Key] = append(groups[p.Key], p)
		}
	}
	decisions := make([]Decision, 0, len(groups))
	for key, candidates := range groups {
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].Scope.rank() != candidates[j].Scope.rank() {
				return candidates[i].Scope.rank() > candidates[j].Scope.rank()
			}
			return candidates[i].Priority > candidates[j].Priority
		})
		d := Decision{Key: key, SelectedID: candidates[0].ID, CandidateIDs: make([]string, 0, len(candidates)), Reason: "only_match"}
		for _, p := range candidates {
			d.CandidateIDs = append(d.CandidateIDs, p.ID)
		}
		if len(candidates) > 1 {
			d.Reason = "higher_priority"
			if candidates[0].Scope.rank() > candidates[1].Scope.rank() {
				d.Reason = "more_specific_scope"
			}
		}
		decisions = append(decisions, d)
	}
	sort.Slice(decisions, func(i, j int) bool { return decisions[i].Key < decisions[j].Key })
	return decisions, nil
}
