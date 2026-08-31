package workintake

import (
	"errors"
	"testing"
)

func TestKindValidate(t *testing.T) {
	tests := []struct {
		name    string
		kind    Kind
		wantErr bool
	}{
		{name: "empty is accepted", kind: "", wantErr: false},
		{name: "bug", kind: KindBug, wantErr: false},
		{name: "feature", kind: KindFeature, wantErr: false},
		{name: "bootstrap", kind: KindBootstrap, wantErr: false},
		{name: "default", kind: KindDefault, wantErr: false},
		{name: "unknown", kind: Kind("nonsense"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.kind.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Validate() error = %v, want nil", err)
			}
			if tt.wantErr && !errors.Is(err, ErrUnknownKind) {
				t.Fatalf("Validate() error = %v, want wrapping ErrUnknownKind", err)
			}
		})
	}
}

func TestSubjectForKind(t *testing.T) {
	tests := []struct {
		name string
		kind Kind
		want string
	}{
		{name: "bug", kind: KindBug, want: SubjectTaskBug},
		{name: "feature", kind: KindFeature, want: SubjectTaskFeature},
		{name: "bootstrap", kind: KindBootstrap, want: SubjectTaskBootstrap},
		{name: "default", kind: KindDefault, want: SubjectTaskDefault},
		{name: "empty falls back to default", kind: "", want: SubjectTaskDefault},
		{name: "unrecognised falls back to default", kind: Kind("nonsense"), want: SubjectTaskDefault},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SubjectForKind(tt.kind); got != tt.want {
				t.Fatalf("SubjectForKind(%q) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

func TestKindForLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		want   Kind
	}{
		{name: "nil labels", labels: nil, want: KindDefault},
		{name: "empty labels", labels: []string{}, want: KindDefault},
		{name: "unrecognised label", labels: []string{"wontfix"}, want: KindDefault},
		{name: "single recognised", labels: []string{"bug"}, want: KindBug},
		{name: "first recognised wins", labels: []string{"feature", "bug"}, want: KindFeature},
		{name: "unrecognised then recognised", labels: []string{"wontfix", "bootstrap"}, want: KindBootstrap},
		{name: "trims whitespace", labels: []string{" bug "}, want: KindBug},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := KindForLabels(tt.labels); got != tt.want {
				t.Fatalf("KindForLabels(%v) = %q, want %q", tt.labels, got, tt.want)
			}
		})
	}
}

func TestKindsForLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		want   []Kind
	}{
		{name: "nil labels", labels: nil, want: nil},
		{name: "no recognised labels", labels: []string{"wontfix", "help wanted"}, want: nil},
		{name: "single", labels: []string{"bug"}, want: []Kind{KindBug}},
		{name: "preserves order", labels: []string{"bug", "feature"}, want: []Kind{KindBug, KindFeature}},
		{name: "skips unrecognised", labels: []string{"wontfix", "bug", "triage", "feature"}, want: []Kind{KindBug, KindFeature}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := KindsForLabels(tt.labels)
			if len(got) != len(tt.want) {
				t.Fatalf("KindsForLabels(%v) = %v, want %v", tt.labels, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("KindsForLabels(%v) = %v, want %v", tt.labels, got, tt.want)
				}
			}
		})
	}
}
