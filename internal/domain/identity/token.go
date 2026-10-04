package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// PersonalTokenIssuer is the subject issuer a personal API token binds under.
// The subject is the token's SHA-256, so the store never holds the token.
const PersonalTokenIssuer = "archie-token"

const personalTokenPrefix = "archie_pat_"

// NewPersonalToken returns a fresh token and the subject it binds as.
func NewPersonalToken() (string, Subject, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", Subject{}, err
	}
	raw := personalTokenPrefix + hex.EncodeToString(secret[:])
	subject, _ := PersonalTokenSubject(raw)
	return raw, subject, nil
}

// PersonalTokenSubject returns the subject a presented token binds as, or
// false when the credential is not a personal token.
func PersonalTokenSubject(raw string) (Subject, bool) {
	if !strings.HasPrefix(raw, personalTokenPrefix) {
		return Subject{}, false
	}
	sum := sha256.Sum256([]byte(raw))
	return Subject{Issuer: PersonalTokenIssuer, Subject: hex.EncodeToString(sum[:])}, true
}

// PersonalToken is one token as its owner sees it: the subject it binds as,
// which identifies it without revealing it, and when it was made.
type PersonalToken struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// PersonalTokenStore is the store side of personal tokens, for one identity.
type PersonalTokenStore interface {
	BindSubject(context.Context, IdentityID, Subject, Audit) error
	SubjectsOf(ctx context.Context, id IdentityID, issuer string) ([]PersonalToken, error)
	UnbindSubject(context.Context, IdentityID, Subject, Audit) error
}

// PersonalTokens is personal tokens as a remote caller sees them: the State
// Store acts on the caller's own principal, so no method names an identity.
type PersonalTokens interface {
	AddPersonalToken(ctx context.Context, subject Subject) error
	ListPersonalTokens(ctx context.Context) ([]PersonalToken, error)
	RevokePersonalToken(ctx context.Context, id string) error
}
