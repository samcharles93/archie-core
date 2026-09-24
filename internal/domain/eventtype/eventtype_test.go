package eventtype

import (
	"errors"
	"reflect"
	"testing"
)

func sample(headers map[string]string, body string) Sample {
	return Sample{Headers: headers, Body: []byte(body)}
}

func TestParseHeaders(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want map[string]string
	}{
		{"http header json", `{"X-Github-Event":["push"],"Content-Type":["application/json"]}`, map[string]string{"x-github-event": "push", "content-type": "application/json"}},
		{"empty", ``, map[string]string{}},
		{"not json", `nope`, map[string]string{}},
		{"empty value list", `{"X-A":[]}`, map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseHeaders(tt.raw); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseHeaders = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSign(t *testing.T) {
	tests := []struct {
		name        string
		in          Sample
		wantPaths   map[string]ValueType
		wantHeaders map[string]string
	}{
		{
			name: "paths with types and discriminator headers",
			in: sample(map[string]string{
				"x-github-event": "pull_request",
				"content-type":   "application/json; charset=utf-8",
				"user-agent":     "GitHub-Hookshot",
			}, `{"action":"opened","number":7,"draft":false,"pull_request":{"labels":[{"name":"x"}],"body":null},"tags":[]}`),
			wantPaths: map[string]ValueType{
				"action":                     TypeString,
				"number":                     TypeNumber,
				"draft":                      TypeBool,
				"pull_request":               TypeObject,
				"pull_request.labels":        TypeArray,
				"pull_request.labels[]":      TypeObject,
				"pull_request.labels[].name": TypeString,
				"pull_request.body":          TypeNull,
				"tags":                       TypeArray,
			},
			wantHeaders: map[string]string{"x-github-event": "pull_request", "content-type": "application/json"},
		},
		{
			name:        "non-json body has no paths",
			in:          sample(map[string]string{"x-kind-type": "blocked"}, `plain text`),
			wantPaths:   map[string]ValueType{},
			wantHeaders: map[string]string{"x-kind-type": "blocked"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Sign(tt.in)
			if !reflect.DeepEqual(got.Paths, tt.wantPaths) {
				t.Errorf("paths = %v, want %v", got.Paths, tt.wantPaths)
			}
			if !reflect.DeepEqual(got.Headers, tt.wantHeaders) {
				t.Errorf("headers = %v, want %v", got.Headers, tt.wantHeaders)
			}
		})
	}
}

func TestSignatureKeyIgnoresValuesButNotShape(t *testing.T) {
	a := Sign(sample(nil, `{"a":1,"b":"x"}`))
	b := Sign(sample(nil, `{"b":"y","a":2}`))
	c := Sign(sample(nil, `{"a":"1","b":"x"}`))
	if a.Key() != b.Key() {
		t.Errorf("same shape, different keys: %q vs %q", a.Key(), b.Key())
	}
	if a.Key() == c.Key() {
		t.Errorf("different types share key %q", a.Key())
	}
}

func TestRuleMatches(t *testing.T) {
	ev := sample(map[string]string{"x-github-event": "pull_request", "content-type": "application/json; charset=utf-8"},
		`{"action":"opened","number":7,"merged":false,"labels":[{"name":"bug"},{"name":"ui"}]}`)
	tests := []struct {
		name string
		rule Rule
		want bool
	}{
		{"empty rule matches", Rule{}, true},
		{"header equal, name case-insensitive", Rule{Headers: []HeaderCondition{{Name: "X-GitHub-Event", Value: "pull_request"}}}, true},
		{"header differs", Rule{Headers: []HeaderCondition{{Name: "X-GitHub-Event", Value: "push"}}}, false},
		{"header absent", Rule{Headers: []HeaderCondition{{Name: "X-Gitea-Event", Value: "push"}}}, false},
		{"content type compared by media type", Rule{Headers: []HeaderCondition{{Name: "Content-Type", Value: "application/json"}}}, true},
		{"path equals string", Rule{Payload: []PayloadCondition{{Path: "action", Op: OpEquals, Value: "opened"}}}, true},
		{"path equals other string", Rule{Payload: []PayloadCondition{{Path: "action", Op: OpEquals, Value: "closed"}}}, false},
		{"path equals number", Rule{Payload: []PayloadCondition{{Path: "number", Op: OpEquals, Value: "7"}}}, true},
		{"path equals bool", Rule{Payload: []PayloadCondition{{Path: "merged", Op: OpEquals, Value: "false"}}}, true},
		{"path present", Rule{Payload: []PayloadCondition{{Path: "number", Op: OpPresent}}}, true},
		{"path missing", Rule{Payload: []PayloadCondition{{Path: "sender.login", Op: OpPresent}}}, false},
		{"array element equals", Rule{Payload: []PayloadCondition{{Path: "labels[].name", Op: OpEquals, Value: "ui"}}}, true},
		{"all conditions must hold", Rule{
			Headers: []HeaderCondition{{Name: "x-github-event", Value: "pull_request"}},
			Payload: []PayloadCondition{{Path: "action", Op: OpEquals, Value: "closed"}},
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rule.Matches(ev); got != tt.want {
				t.Fatalf("Matches = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOverlaps(t *testing.T) {
	gh := func(v string) HeaderCondition { return HeaderCondition{Name: "X-GitHub-Event", Value: v} }
	eq := func(p, v string) PayloadCondition { return PayloadCondition{Path: p, Op: OpEquals, Value: v} }
	present := func(p string) PayloadCondition { return PayloadCondition{Path: p, Op: OpPresent} }
	tests := []struct {
		name string
		a, b Rule
		want bool
	}{
		{"different header values are disjoint", Rule{Headers: []HeaderCondition{gh("push")}}, Rule{Headers: []HeaderCondition{gh("pull_request")}}, false},
		{"header names compared case-insensitively", Rule{Headers: []HeaderCondition{gh("push")}}, Rule{Headers: []HeaderCondition{{Name: "x-github-event", Value: "issues"}}}, false},
		{"same header overlaps", Rule{Headers: []HeaderCondition{gh("push")}}, Rule{Headers: []HeaderCondition{gh("push")}}, true},
		{"empty rule overlaps everything", Rule{}, Rule{Headers: []HeaderCondition{gh("push")}}, true},
		{"different equals on a path are disjoint", Rule{Payload: []PayloadCondition{eq("action", "opened")}}, Rule{Payload: []PayloadCondition{eq("action", "closed")}}, false},
		{"equals and present on one path overlap", Rule{Payload: []PayloadCondition{eq("action", "opened")}}, Rule{Payload: []PayloadCondition{present("action")}}, true},
		{"unrelated conditions overlap", Rule{Payload: []PayloadCondition{present("a")}}, Rule{Payload: []PayloadCondition{present("b")}}, true},
		{"array paths never prove disjointness", Rule{Payload: []PayloadCondition{eq("l[].n", "x")}}, Rule{Payload: []PayloadCondition{eq("l[].n", "y")}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Overlaps(tt.a, tt.b); got != tt.want {
				t.Fatalf("Overlaps = %v, want %v", got, tt.want)
			}
			if got := Overlaps(tt.b, tt.a); got != tt.want {
				t.Fatalf("Overlaps (swapped) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEventTypeValidate(t *testing.T) {
	ok := EventType{Source: "gh", Name: "push", Rule: Rule{Headers: []HeaderCondition{{Name: "X-GitHub-Event", Value: "push"}}}}
	tests := []struct {
		name   string
		mutate func(*EventType)
		valid  bool
	}{
		{"valid", func(*EventType) {}, true},
		{"missing name", func(e *EventType) { e.Name = " " }, false},
		{"missing source", func(e *EventType) { e.Source = "" }, false},
		{"header without name", func(e *EventType) { e.Rule.Headers = []HeaderCondition{{Value: "x"}} }, false},
		{"payload without path", func(e *EventType) { e.Rule.Payload = []PayloadCondition{{Op: OpPresent}} }, false},
		{"unknown operator", func(e *EventType) { e.Rule.Payload = []PayloadCondition{{Path: "a", Op: "like"}} }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := ok
			e.Rule = Rule{Headers: append([]HeaderCondition(nil), ok.Rule.Headers...)}
			tt.mutate(&e)
			err := e.Validate()
			if (err == nil) != tt.valid {
				t.Fatalf("Validate = %v, valid want %v", err, tt.valid)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("Validate error %v is not ErrInvalid", err)
			}
		})
	}
}

func TestCheckOverlap(t *testing.T) {
	push := EventType{ID: "1", Source: "gh", Name: "push", Rule: Rule{Headers: []HeaderCondition{{Name: "X-GitHub-Event", Value: "push"}}}}
	tests := []struct {
		name      string
		candidate EventType
		wantErr   bool
	}{
		{"disjoint on same source", EventType{Source: "gh", Name: "pr", Rule: Rule{Headers: []HeaderCondition{{Name: "X-GitHub-Event", Value: "pull_request"}}}}, false},
		{"overlapping on same source", EventType{Source: "gh", Name: "any", Rule: Rule{}}, true},
		{"overlapping on another source", EventType{Source: "fw", Name: "any", Rule: Rule{}}, false},
		{"an update does not overlap itself", EventType{ID: "1", Source: "gh", Name: "push2", Rule: Rule{}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckOverlap([]EventType{push}, tt.candidate)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckOverlap = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrOverlap) {
				t.Fatalf("error %v is not ErrOverlap", err)
			}
		})
	}
}

func TestIdentify(t *testing.T) {
	types := []EventType{
		{ID: "push", Source: "gh", Name: "push", Rule: Rule{Headers: []HeaderCondition{{Name: "X-GitHub-Event", Value: "push"}}}},
		{ID: "pr", Source: "gh", Name: "pr", Rule: Rule{Headers: []HeaderCondition{{Name: "X-GitHub-Event", Value: "pull_request"}}}},
		{ID: "fw", Source: "fw", Name: "any", Rule: Rule{}},
	}
	tests := []struct {
		name   string
		source string
		in     Sample
		want   string
	}{
		{"matches its type", "gh", sample(map[string]string{"x-github-event": "push"}, `{}`), "push"},
		{"no type matches", "gh", sample(map[string]string{"x-github-event": "issues"}, `{}`), ""},
		{"types from other sources are ignored", "other", sample(nil, `{}`), ""},
		{"catch-all on its source", "fw", sample(nil, `{"x":1}`), "fw"},
		{"two matches is unidentified", "gh", sample(map[string]string{"x-github-event": "push"}, `{}`), ""},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := types
			if i == len(tests)-1 {
				ts = append(ts, EventType{ID: "dup", Source: "gh", Name: "dup", Rule: Rule{}})
			}
			got, ok := Identify(ts, tt.source, tt.in)
			if got.ID != tt.want || ok != (tt.want != "") {
				t.Fatalf("Identify = %q,%v want %q", got.ID, ok, tt.want)
			}
		})
	}
}

func TestFromExample(t *testing.T) {
	tests := []struct {
		name     string
		in       Sample
		wantRule Rule
	}{
		{
			name:     "discriminator headers become the rule",
			in:       sample(map[string]string{"X-GitHub-Event": "push", "User-Agent": "x"}, `{"ref":"main"}`),
			wantRule: Rule{Headers: []HeaderCondition{{Name: "x-github-event", Value: "push"}}},
		},
		{
			name:     "without discriminators the top-level paths must be present",
			in:       sample(nil, `{"src":"1.2.3.4","action":{"k":1}}`),
			wantRule: Rule{Payload: []PayloadCondition{{Path: "action", Op: OpPresent}, {Path: "src", Op: OpPresent}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FromExample("s", "n", tt.in)
			if !reflect.DeepEqual(got.Rule, tt.wantRule) {
				t.Fatalf("rule = %+v, want %+v", got.Rule, tt.wantRule)
			}
			if !reflect.DeepEqual(got.Schema, Sign(tt.in).Paths) {
				t.Fatalf("schema = %v, want the example's paths", got.Schema)
			}
			if !got.Rule.Matches(tt.in) {
				t.Fatal("a type made from an example must match that example")
			}
		})
	}
}

func TestPropose(t *testing.T) {
	gh := func(id, ev, body string) Capture {
		return Capture{ID: id, Source: "gh", Sample: sample(map[string]string{"x-github-event": ev}, body)}
	}
	captures := []Capture{
		gh("c1", "push", `{"ref":"a"}`),
		gh("c2", "pull_request", `{"action":"opened"}`),
		gh("c3", "push", `{"ref":"b"}`),
		{ID: "f1", Source: "fw", Sample: sample(nil, `{"src":"1.1.1.1","port":22}`)},
		{ID: "f2", Source: "fw", Sample: sample(nil, `{"src":"1.1.1.1","port":22,"extra":true}`)},
	}
	got := Propose(captures)
	type summary struct {
		source, sample string
		count          int
		rule           Rule
	}
	var sums []summary
	for _, p := range got {
		sums = append(sums, summary{p.Source, p.SampleID, p.Count, p.Rule})
	}
	want := []summary{
		{"fw", "f1", 1, Rule{}},
		{"fw", "f2", 1, Rule{Payload: []PayloadCondition{{Path: "extra", Op: OpPresent}}}},
		{"gh", "c1", 2, Rule{Headers: []HeaderCondition{{Name: "x-github-event", Value: "push"}}}},
		{"gh", "c2", 1, Rule{Headers: []HeaderCondition{{Name: "x-github-event", Value: "pull_request"}}}},
	}
	if !reflect.DeepEqual(sums, want) {
		t.Fatalf("Propose =\n%+v\nwant\n%+v", sums, want)
	}
	for _, p := range got {
		if len(p.Schema) == 0 {
			t.Errorf("proposal %s has no schema", p.SampleID)
		}
	}
}
