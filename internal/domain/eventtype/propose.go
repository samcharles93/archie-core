package eventtype

import (
	"maps"
	"slices"
	"strings"
	"time"
)

// Capture is one stored event as proposal grouping reads it.
type Capture struct {
	ID         string
	Source     string
	ReceivedAt time.Time
	Sample     Sample
}

// Proposal is a group of one source's captures sharing a signature, offered
// to the operator as an event type to name. Rule is the set of
// discriminators that separates the group from its siblings on the source.
type Proposal struct {
	Source   string               `json:"source"`
	Key      string               `json:"key"`
	Schema   map[string]ValueType `json:"schema"`
	Headers  map[string]string    `json:"headers"`
	Count    int                  `json:"count"`
	SampleID string               `json:"sample_id"`
	Rule     Rule                 `json:"rule"`
}

// Propose groups captures by source and signature. Groups are ordered by
// source, then largest first, then first seen; the sample is the group's first
// capture in input order.
func Propose(captures []Capture) []Proposal {
	var groups []*Proposal
	sigs := map[*Proposal]Signature{}
	index := map[string]*Proposal{}
	for _, c := range captures {
		sig := Sign(c.Sample)
		key := sig.Key()
		id := c.Source + "\x00" + key
		if g, ok := index[id]; ok {
			g.Count++
			continue
		}
		g := &Proposal{Source: c.Source, Key: key, Schema: sig.Paths, Headers: sig.Headers, Count: 1, SampleID: c.ID}
		index[id] = g
		sigs[g] = sig
		groups = append(groups, g)
	}
	slices.SortStableFunc(groups, func(a, b *Proposal) int {
		if c := strings.Compare(a.Source, b.Source); c != 0 {
			return c
		}
		return b.Count - a.Count
	})
	out := make([]Proposal, 0, len(groups))
	for _, g := range groups {
		var siblings []Signature
		for _, s := range groups {
			if s != g && s.Source == g.Source {
				siblings = append(siblings, sigs[s])
			}
		}
		g.Rule = separatingRule(sigs[g], siblings)
		out = append(out, *g)
	}
	return out
}

// separatingRule demands the group's discriminator headers, then, for each
// sibling those do not already rule out, the presence of the first path the
// group has and the sibling lacks. A group whose paths are a subset of a
// sibling's cannot be separated by presence alone; saving it as proposed is
// then refused as an overlap, and the operator edits the rule.
func separatingRule(g Signature, siblings []Signature) Rule {
	rule := Rule{Headers: headerConditions(g.Headers)}
	paths := slices.Sorted(maps.Keys(g.Paths))
	for _, s := range siblings {
		if headersSeparate(g.Headers, s.Headers) || rulePathsSeparate(rule, s) {
			continue
		}
		for _, path := range paths {
			if _, ok := s.Paths[path]; !ok {
				rule.Payload = append(rule.Payload, PayloadCondition{Path: path, Op: OpPresent})
				break
			}
		}
	}
	return rule
}

func headersSeparate(g, s map[string]string) bool {
	for name, value := range g {
		if other, ok := s[name]; !ok || other != value {
			return true
		}
	}
	return false
}

func rulePathsSeparate(rule Rule, s Signature) bool {
	for _, p := range rule.Payload {
		if _, ok := s.Paths[p.Path]; !ok {
			return true
		}
	}
	return false
}
