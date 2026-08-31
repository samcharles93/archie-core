package workintake

import (
	"strings"
	"testing"
)

func TestSplitLabels(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty", raw: "", want: nil},
		{name: "single", raw: "bug", want: []string{"bug"}},
		{name: "multiple", raw: "bug,feature", want: []string{"bug", "feature"}},
		{name: "drops empties", raw: "bug,,feature", want: []string{"bug", "feature"}},
		{name: "trailing comma", raw: "bug,", want: []string{"bug"}},
		{name: "trims whitespace", raw: " bug , feature ", want: []string{"bug", "feature"}},
		{name: "only commas", raw: ",,,", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitLabels(tt.raw)
			if len(got) != len(tt.want) {
				t.Fatalf("SplitLabels(%q) = %v, want %v", tt.raw, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("SplitLabels(%q) = %v, want %v", tt.raw, got, tt.want)
				}
			}
		})
	}
}

func TestTaskEnvelopeMarshalRoundTrip(t *testing.T) {
	original := TaskEnvelope{
		Owner:    "samcharles93",
		Repo:     "archie-core",
		Number:   42,
		Title:    "fix the thing",
		Body:     "it's broken",
		Labels:   []string{"bug", "P1"},
		Identity: "archie-bot",
		Kind:     KindBug,
	}

	data, err := original.Encode()
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	decoded, err := DecodeTask(data)
	if err != nil {
		t.Fatalf("DecodeTask() error = %v", err)
	}

	if decoded.Owner != original.Owner || decoded.Repo != original.Repo || decoded.Number != original.Number {
		t.Fatalf("round trip identity mismatch: got %+v, want %+v", decoded, original)
	}
	if decoded.Title != original.Title || decoded.Body != original.Body {
		t.Fatalf("round trip content mismatch: got %+v, want %+v", decoded, original)
	}
	if len(decoded.Labels) != len(original.Labels) {
		t.Fatalf("round trip labels = %v, want %v", decoded.Labels, original.Labels)
	}
	for i := range decoded.Labels {
		if decoded.Labels[i] != original.Labels[i] {
			t.Fatalf("round trip labels = %v, want %v", decoded.Labels, original.Labels)
		}
	}
	if decoded.Identity != original.Identity {
		t.Fatalf("round trip Identity = %q, want %q", decoded.Identity, original.Identity)
	}
	if decoded.Kind != original.Kind {
		t.Fatalf("round trip Kind = %q, want %q", decoded.Kind, original.Kind)
	}
}

func TestTaskEnvelopeMarshalFlattensLabels(t *testing.T) {
	envelope := TaskEnvelope{Owner: "o", Repo: "r", Number: 1, Labels: []string{"bug", "feature"}}
	data, err := envelope.Encode()
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if !strings.Contains(string(data), `"labels":"bug,feature"`) {
		t.Fatalf("Encode() = %s, want labels flattened to comma-separated wire field", data)
	}
}

func TestDecodeTaskLegacyMessageWithoutKind(t *testing.T) {
	// Messages queued before Kind existed decode with the zero value, which
	// must behave as KindDefault.
	legacy := `{"owner":"o","repo":"r","number":1,"title":"t","body":"b","labels":"bug"}`
	decoded, err := DecodeTask([]byte(legacy))
	if err != nil {
		t.Fatalf("DecodeTask() error = %v", err)
	}
	if decoded.Kind != "" {
		t.Fatalf("Kind = %q, want empty (decodes as KindDefault)", decoded.Kind)
	}
	if decoded.Subject() != SubjectTaskDefault {
		t.Fatalf("Subject() = %q, want %q", decoded.Subject(), SubjectTaskDefault)
	}
	if len(decoded.Labels) != 1 || decoded.Labels[0] != "bug" {
		t.Fatalf("Labels = %v, want [bug]", decoded.Labels)
	}
}

func TestDecodeTaskInvalidJSON(t *testing.T) {
	if _, err := DecodeTask([]byte("not json")); err == nil {
		t.Fatal("DecodeTask() error = nil, want error for invalid JSON")
	}
}

func TestTaskEnvelopeRef(t *testing.T) {
	e := TaskEnvelope{Owner: "samcharles93", Repo: "archie-core", Number: 42}
	if got, want := e.Ref(), "samcharles93/archie-core#42"; got != want {
		t.Fatalf("Ref() = %q, want %q", got, want)
	}
}

func TestTaskEnvelopeIdempotencyKey(t *testing.T) {
	e := TaskEnvelope{Owner: "samcharles93", Repo: "archie-core", Number: 42}
	if got, want := e.IdempotencyKey(), "archie:samcharles93/archie-core/42"; got != want {
		t.Fatalf("IdempotencyKey() = %q, want %q", got, want)
	}
}

func TestTaskEnvelopeSubject(t *testing.T) {
	tests := []struct {
		name string
		kind Kind
		want string
	}{
		{name: "bug", kind: KindBug, want: SubjectTaskBug},
		{name: "feature", kind: KindFeature, want: SubjectTaskFeature},
		{name: "bootstrap", kind: KindBootstrap, want: SubjectTaskBootstrap},
		{name: "default", kind: KindDefault, want: SubjectTaskDefault},
		{name: "empty", kind: "", want: SubjectTaskDefault},
		{name: "unknown", kind: Kind("nonsense"), want: SubjectTaskDefault},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := TaskEnvelope{Kind: tt.kind}
			if got := e.Subject(); got != tt.want {
				t.Fatalf("Subject() = %q, want %q", got, tt.want)
			}
		})
	}
}
