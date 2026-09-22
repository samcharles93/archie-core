package identity

import (
	"context"
	"errors"
	"testing"
)

type stubVerifier struct {
	credential Credential
	err        error
}

func (s stubVerifier) Verify(context.Context, string) (Credential, error) {
	return s.credential, s.err
}

type stubSubjects struct {
	value Identity
	err   error
}

func (s stubSubjects) ResolveSubject(context.Context, Subject) (Identity, error) {
	return s.value, s.err
}

func testIdentity(kind Kind, lifecycle Lifecycle) Identity {
	return Identity{ID: "10000000-0000-5000-8000-000000000001", Kind: kind, DisplayName: "tester", Lifecycle: lifecycle, Version: 1}
}

func TestAuthenticateRefusesEveryUnprovenCaller(t *testing.T) {
	verified := Credential{Subject: Subject{Issuer: "https://idp.example", Subject: "sam"}}
	rejected := errors.New("signature verification failed")

	tests := []struct {
		name     string
		rawToken string
		verifier Verifier
		subjects SubjectResolver
		want     error
	}{
		{
			name:     "no credential presented",
			rawToken: "",
			verifier: stubVerifier{credential: verified},
			subjects: stubSubjects{value: testIdentity(KindUser, LifecycleActive)},
			want:     ErrNoCredential,
		},
		{
			name:     "whitespace is not a credential",
			rawToken: "   ",
			verifier: stubVerifier{credential: verified},
			subjects: stubSubjects{value: testIdentity(KindUser, LifecycleActive)},
			want:     ErrNoCredential,
		},
		{
			name:     "no provider is configured, so nothing can be verified",
			rawToken: "token",
			verifier: nil,
			subjects: stubSubjects{value: testIdentity(KindUser, LifecycleActive)},
			want:     ErrCredentialRejected,
		},
		{
			name:     "token signed by the wrong key is rejected",
			rawToken: "token",
			verifier: stubVerifier{err: rejected},
			subjects: stubSubjects{value: testIdentity(KindUser, LifecycleActive)},
			want:     ErrCredentialRejected,
		},
		{
			name:     "verified credential with no identity bound to its subject",
			rawToken: "token",
			verifier: stubVerifier{credential: verified},
			subjects: stubSubjects{err: ErrNotFound},
			want:     ErrSubjectUnbound,
		},
		{
			name:     "verified credential for a suspended identity",
			rawToken: "token",
			verifier: stubVerifier{credential: verified},
			subjects: stubSubjects{value: testIdentity(KindBot, LifecycleSuspended)},
			want:     ErrIdentityInactive,
		},
		{
			name:     "verified credential for a retired identity",
			rawToken: "token",
			verifier: stubVerifier{credential: verified},
			subjects: stubSubjects{value: testIdentity(KindBot, LifecycleRetired)},
			want:     ErrIdentityInactive,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := Authenticate(context.Background(), tc.subjects, tc.verifier, tc.rawToken)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Authenticate() error = %v, want %v", err, tc.want)
			}
			if got != (Identity{}) {
				t.Fatalf("Authenticate() returned identity %+v on a refusal", got)
			}
		})
	}
}

func TestAuthenticateResolvesAnActiveIdentity(t *testing.T) {
	want := testIdentity(KindBot, LifecycleActive)
	verified := Credential{
		Subject: Subject{Issuer: "https://idp.example", Subject: "archie-agent"},
		Scopes:  []string{"tasks:write"},
	}

	got, credential, err := Authenticate(context.Background(), stubSubjects{value: want}, stubVerifier{credential: verified}, "token")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if got != want {
		t.Fatalf("Authenticate() identity = %+v, want %+v", got, want)
	}
	if credential.Subject != verified.Subject {
		t.Fatalf("Authenticate() credential subject = %+v, want %+v", credential.Subject, verified.Subject)
	}
}

func TestCanActOnlyForAnActiveIdentity(t *testing.T) {
	tests := []struct {
		lifecycle Lifecycle
		want      bool
	}{
		{LifecycleActive, true},
		{LifecycleSuspended, false},
		{LifecycleRetired, false},
	}
	for _, tc := range tests {
		value := testIdentity(KindUser, tc.lifecycle)
		if got := value.CanAct(); got != tc.want {
			t.Errorf("Identity{%s}.CanAct() = %t, want %t", tc.lifecycle, got, tc.want)
		}
	}
}

func TestSubjectRequiresBothHalves(t *testing.T) {
	tests := []struct {
		name    string
		subject Subject
		wantErr bool
	}{
		{"complete", Subject{Issuer: "https://idp.example", Subject: "sam"}, false},
		{"no issuer", Subject{Subject: "sam"}, true},
		{"no subject", Subject{Issuer: "https://idp.example"}, true},
		{"blank", Subject{Issuer: " ", Subject: " "}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.subject.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("Subject.Validate() error = %v, wantErr %t", err, tc.wantErr)
			}
		})
	}
}
