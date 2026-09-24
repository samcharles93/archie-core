package source

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNewDefaultsToSignedUUIDv7Path(t *testing.T) {
	s, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	id, err := uuid.Parse(s.Path)
	if err != nil {
		t.Fatalf("path %q is not a UUID: %v", s.Path, err)
	}
	if id.Version() != 7 {
		t.Fatalf("path version = %d, want 7", id.Version())
	}
	if s.Signing != SigningSigned {
		t.Fatalf("signing = %q, want %q", s.Signing, SigningSigned)
	}
	if len(s.Secret) < MinSecretLen {
		t.Fatalf("secret length = %d, want >= %d", len(s.Secret), MinSecretLen)
	}
	other, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if other.Path == s.Path || other.Secret == s.Secret {
		t.Fatal("two new sources share a path or a secret")
	}
}

func TestNewAcceptsOrRefusesCustomPath(t *testing.T) {
	tests := []struct {
		path string
		ok   bool
	}{
		{"sentry", true},
		{"fire-wall_01.v2~x", true},
		{"a/b", false},
		{"with space", false},
		{"percent%20", false},
		{"..", false},
		{".", false},
		{strings.Repeat("a", MaxPathLen+1), false},
		{"ünicode", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			s, err := New(tt.path)
			if tt.ok {
				if err != nil || s.Path != tt.path {
					t.Fatalf("New(%q) = %q, %v; want path kept", tt.path, s.Path, err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidPath) {
				t.Fatalf("New(%q) error = %v, want ErrInvalidPath", tt.path, err)
			}
		})
	}
}

func TestSigningTransitions(t *testing.T) {
	tests := []struct {
		name    string
		from    Signing
		action  func(*Source) error
		want    Signing
		wantErr bool
	}{
		{"request unsigned waits for approval", SigningSigned, (*Source).RequestUnsigned, SigningUnsignedPending, false},
		{"approve pending", SigningUnsignedPending, (*Source).ApproveUnsigned, SigningUnsigned, false},
		{"approve without request", SigningSigned, (*Source).ApproveUnsigned, SigningSigned, true},
		{"approve twice", SigningUnsigned, (*Source).ApproveUnsigned, SigningUnsigned, true},
		{"resign from unsigned", SigningUnsigned, (*Source).RequireSigning, SigningSigned, false},
		{"resign from pending", SigningUnsignedPending, (*Source).RequireSigning, SigningSigned, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Source{Path: "p", Signing: tt.from}
			err := tt.action(&s)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if s.Signing != tt.want {
				t.Fatalf("signing = %q, want %q", s.Signing, tt.want)
			}
		})
	}
}

func TestUnsignedOnlyOnceApproved(t *testing.T) {
	for signing, want := range map[Signing]bool{
		SigningSigned:          false,
		SigningUnsignedPending: false,
		SigningUnsigned:        true,
	} {
		if got := (&Source{Signing: signing}).Unsigned(); got != want {
			t.Errorf("Unsigned() for %q = %v, want %v", signing, got, want)
		}
	}
}
