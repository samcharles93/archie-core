// Package eventtype owns event types: named kinds of event from one source,
// each defined by a match rule over headers and payload. It infers a
// structural signature per capture, groups a source's captures into proposed
// types, evaluates rules, and refuses two types on one source whose rules can
// match the same event. An event that matches no type is unidentified and is
// never dispatched. See docs/prds/event-automation.md, "Event types".
package eventtype

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

var (
	// ErrInvalid marks an event type that cannot be saved as written.
	ErrInvalid = errors.New("eventtype: invalid event type")
	// ErrOverlap marks an event type whose rule can match an event another
	// type on the same source also matches.
	ErrOverlap = errors.New("eventtype: rule overlaps another event type on the source")
)

// Operator is a payload condition's test.
type Operator string

const (
	OpEquals  Operator = "equals"
	OpPresent Operator = "present"
)

// HeaderCondition holds when the named header (case-insensitive) has Value.
type HeaderCondition struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// PayloadCondition holds when Path is present or, for OpEquals, when a value
// at Path renders as Value ("7", "true", "null", or the string itself).
type PayloadCondition struct {
	Path  string   `json:"path"`
	Op    Operator `json:"op"`
	Value string   `json:"value,omitempty"`
}

// Rule is a conjunction: every condition must hold. An empty rule matches
// every event from its source.
type Rule struct {
	Headers []HeaderCondition  `json:"headers"`
	Payload []PayloadCondition `json:"payload"`
}

// EventType is a named kind of event from one source. Schema is the inferred
// structure (path to type) of the payload it was created from.
type EventType struct {
	ID        string               `json:"id"`
	Source    string               `json:"source"`
	Name      string               `json:"name"`
	Rule      Rule                 `json:"rule"`
	Schema    map[string]ValueType `json:"schema"`
	CreatedAt time.Time            `json:"created_at"`
	UpdatedAt time.Time            `json:"updated_at"`
}

// Validate refuses an event type that has no name or source or carries a
// malformed condition.
func (t EventType) Validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if strings.TrimSpace(t.Source) == "" {
		return fmt.Errorf("%w: source is required", ErrInvalid)
	}
	for _, h := range t.Rule.Headers {
		if strings.TrimSpace(h.Name) == "" {
			return fmt.Errorf("%w: header condition has no name", ErrInvalid)
		}
	}
	for _, p := range t.Rule.Payload {
		if strings.TrimSpace(p.Path) == "" {
			return fmt.Errorf("%w: payload condition has no path", ErrInvalid)
		}
		if p.Op != OpEquals && p.Op != OpPresent {
			return fmt.Errorf("%w: unknown operator %q", ErrInvalid, p.Op)
		}
	}
	return nil
}

// Matches reports whether every condition of r holds for s.
func (r Rule) Matches(s Sample) bool {
	for _, h := range r.Headers {
		got, ok := headerLookup(s.Headers, h.Name)
		if !ok || headerValue(h.Name, got) != headerValue(h.Name, h.Value) {
			return false
		}
	}
	if len(r.Payload) == 0 {
		return true
	}
	root, ok := decode(s.Body)
	if !ok {
		return false
	}
	for _, p := range r.Payload {
		if !payloadHolds(root, p) {
			return false
		}
	}
	return true
}

func headerLookup(headers map[string]string, name string) (string, bool) {
	for k, v := range headers {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

func payloadHolds(root any, p PayloadCondition) bool {
	values := lookup(root, p.Path)
	if p.Op == OpPresent {
		return len(values) > 0
	}
	for _, v := range values {
		if text, ok := scalarText(v); ok && text == p.Value {
			return true
		}
	}
	return false
}

// Overlaps reports whether some event could satisfy both rules. Two rules are
// disjoint only when they demand different values of one header, or different
// values at one payload path outside any array; anything else is treated as
// overlapping, which errs towards refusing a save rather than towards an event
// two types both claim.
func Overlaps(a, b Rule) bool {
	for _, ha := range a.Headers {
		for _, hb := range b.Headers {
			if strings.EqualFold(ha.Name, hb.Name) && headerValue(ha.Name, ha.Value) != headerValue(hb.Name, hb.Value) {
				return false
			}
		}
	}
	for _, pa := range a.Payload {
		for _, pb := range b.Payload {
			if pa.Op == OpEquals && pb.Op == OpEquals && pa.Path == pb.Path &&
				!strings.Contains(pa.Path, "[]") && pa.Value != pb.Value {
				return false
			}
		}
	}
	return true
}

// CheckOverlap refuses candidate when its rule overlaps another type on the
// same source. A type is not compared with itself, so an update passes.
func CheckOverlap(existing []EventType, candidate EventType) error {
	for _, t := range existing {
		if t.Source != candidate.Source || (candidate.ID != "" && t.ID == candidate.ID) {
			continue
		}
		if Overlaps(t.Rule, candidate.Rule) {
			return fmt.Errorf("%w: %q", ErrOverlap, t.Name)
		}
	}
	return nil
}

// Identify returns the one type on source whose rule matches s. No match, or
// more than one (which overlap refusal prevents), is unidentified.
func Identify(types []EventType, source string, s Sample) (EventType, bool) {
	var found []EventType
	for _, t := range types {
		if t.Source == source && t.Rule.Matches(s) {
			found = append(found, t)
		}
	}
	if len(found) != 1 {
		return EventType{}, false
	}
	return found[0], true
}

// FromExample builds an event type from a pasted payload: its schema is the
// example's structure, and its rule is the example's discriminator headers or,
// when it has none, the presence of each top-level payload key.
func FromExample(source, name string, s Sample) EventType {
	sig := Sign(s)
	t := EventType{Source: source, Name: name, Schema: sig.Paths}
	t.Rule.Headers = headerConditions(sig.Headers)
	if len(t.Rule.Headers) == 0 {
		for _, path := range slices.Sorted(maps.Keys(sig.Paths)) {
			if !strings.ContainsAny(path, ".[") {
				t.Rule.Payload = append(t.Rule.Payload, PayloadCondition{Path: path, Op: OpPresent})
			}
		}
	}
	return t
}

func headerConditions(headers map[string]string) []HeaderCondition {
	var out []HeaderCondition
	for _, name := range slices.Sorted(maps.Keys(headers)) {
		out = append(out, HeaderCondition{Name: name, Value: headers[name]})
	}
	return out
}
