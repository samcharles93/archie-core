package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Subject is what an identity provider asserts about a caller. Issuer is part
// of the key because two providers can assert the same subject string for
// different people, and a binding keyed on the subject alone would accept one
// for the other.
type Subject struct {
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
}

func (s Subject) Validate() error {
	if strings.TrimSpace(s.Issuer) == "" || strings.TrimSpace(s.Subject) == "" {
		return fmt.Errorf("%w: issuer and subject are required", ErrInvalid)
	}
	return nil
}

// Credential is what a provider-issued token proved. It is the result of
// verification, never an assertion from the caller.
type Credential struct {
	Subject Subject
	Scopes  []string
	Expires time.Time
}

// Verifier checks a presented bearer token against the identity provider's
// published keys. Implementations hold no secret and mint nothing: they can
// confirm an identity the provider issued, and can never produce one.
type Verifier interface {
	Verify(ctx context.Context, rawToken string) (Credential, error)
}

// SubjectResolver resolves a verified provider subject to the identity bound to
// it. It is deliberately narrower than Repository: authenticating a request
// needs to resolve a subject, not to administer identities, so a consumer that
// only authenticates does not take a dependency on the whole identity CRUD
// surface.
type SubjectResolver interface {
	// ResolveSubject returns the identity a provider subject is bound to. It is
	// the second binding beside the legacy-name alias: a name archie was
	// configured with resolves to an identity, and a subject a provider asserts
	// resolves to the same identity.
	ResolveSubject(context.Context, Subject) (Identity, error)
}

var (
	// ErrNoCredential means the request presented nothing to authenticate with.
	ErrNoCredential = errors.New("no credential presented")
	// ErrCredentialRejected means a credential was presented and did not
	// verify: wrong signature, expired, wrong issuer, wrong audience, or
	// malformed. It never means the credential was merely unrecognised.
	ErrCredentialRejected = errors.New("credential rejected")
	// ErrSubjectUnbound means a verified subject has no identity bound to it.
	// The provider authenticated someone archie does not know.
	ErrSubjectUnbound = errors.New("credential subject is not bound to an identity")
	// ErrIdentityInactive means the identity the credential belongs to may not
	// act: suspended or retired. Authentication succeeded and authorisation
	// did not, which is why it is a distinct refusal from a bad credential.
	ErrIdentityInactive = errors.New("identity may not act")
)

// Authenticate resolves a presented credential to the identity that may act.
//
// The order is deliberate: the credential is verified by the provider's own
// keys before anything it contains is used, so no field of an unverified token
// can select an identity. A suspended identity is refused even though its
// credential verified, because suspension is archie's decision and must not
// depend on the provider withdrawing a token.
func Authenticate(ctx context.Context, subjects SubjectResolver, verifier Verifier, rawToken string) (Identity, Credential, error) {
	if strings.TrimSpace(rawToken) == "" {
		return Identity{}, Credential{}, ErrNoCredential
	}
	if verifier == nil {
		return Identity{}, Credential{}, fmt.Errorf("%w: no identity provider is configured", ErrCredentialRejected)
	}
	credential, err := verifier.Verify(ctx, rawToken)
	if err != nil {
		return Identity{}, Credential{}, fmt.Errorf("%w: %w", ErrCredentialRejected, err)
	}
	if err := credential.Subject.Validate(); err != nil {
		return Identity{}, Credential{}, fmt.Errorf("%w: %w", ErrCredentialRejected, err)
	}
	value, err := subjects.ResolveSubject(ctx, credential.Subject)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Identity{}, Credential{}, fmt.Errorf("%w: %s", ErrSubjectUnbound, credential.Subject.Subject)
		}
		return Identity{}, Credential{}, err
	}
	if !value.CanAct() {
		return Identity{}, Credential{}, fmt.Errorf("%w: %s is %s", ErrIdentityInactive, value.ID, value.Lifecycle)
	}
	return value, credential, nil
}

// CanAct reports whether an identity may perform actions. Only an active
// identity may: a suspended identity is suspended from acting, and a retired
// one is kept for attribution, not for use.
func (i Identity) CanAct() bool { return i.Lifecycle == LifecycleActive }
