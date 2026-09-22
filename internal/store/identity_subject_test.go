package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/events"
)

func bindAudit(requestID string) identity.Audit {
	return identity.Audit{ActorID: identity.SystemID, Source: "test", RequestID: requestID, At: time.Now().UTC()}
}

func newTestIdentity(t *testing.T, s *Store, id identity.IdentityID, kind identity.Kind, name string) identity.Identity {
	t.Helper()
	value, err := identity.New(id, kind, name)
	if err != nil {
		t.Fatalf("identity.New() error = %v", err)
	}
	created, err := s.Create(context.Background(), value, bindAudit("create:"+string(id)))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return created
}

// TestResolveSubjectReturnsTheBoundIdentity: a verified provider subject selects
// the identity it is bound to, which is what lets a credential resolve to a
// record archie already knows rather than creating one.
func TestResolveSubjectReturnsTheBoundIdentity(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	agent := newTestIdentity(t, s, identity.IdentityID("30000000-0000-5000-8000-000000000001"), identity.KindBot, "archie-agent")
	subject := identity.Subject{Issuer: "https://auth.catlow.cloud", Subject: "archie"}

	if err := s.BindSubject(ctx, agent.ID, subject, bindAudit("bind:1")); err != nil {
		t.Fatalf("BindSubject() error = %v", err)
	}

	got, err := s.ResolveSubject(ctx, subject)
	if err != nil {
		t.Fatalf("ResolveSubject() error = %v", err)
	}
	if got.ID != agent.ID || got.Kind != identity.KindBot {
		t.Fatalf("ResolveSubject() = %+v, want %+v", got, agent)
	}
}

// TestResolveSubjectIsKeyedOnIssuerAndSubject: the same subject string from a
// different issuer is a different caller, so it must not resolve.
func TestResolveSubjectIsKeyedOnIssuerAndSubject(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	agent := newTestIdentity(t, s, identity.IdentityID("30000000-0000-5000-8000-000000000002"), identity.KindBot, "archie-agent")
	bound := identity.Subject{Issuer: "https://auth.catlow.cloud", Subject: "archie"}
	if err := s.BindSubject(ctx, agent.ID, bound, bindAudit("bind:2")); err != nil {
		t.Fatalf("BindSubject() error = %v", err)
	}

	others := []struct {
		name    string
		subject identity.Subject
	}{
		{"another issuer, same subject", identity.Subject{Issuer: "https://elsewhere.example", Subject: "archie"}},
		{"same issuer, another subject", identity.Subject{Issuer: "https://auth.catlow.cloud", Subject: "someone-else"}},
	}
	for _, tc := range others {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.ResolveSubject(ctx, tc.subject); !errors.Is(err, identity.ErrNotFound) {
				t.Fatalf("ResolveSubject() error = %v, want %v", err, identity.ErrNotFound)
			}
		})
	}
}

// TestBindSubjectMovesAnAlreadyBoundSubject: when a subject changes hands it
// resolves to whoever holds it now, so a reassignment cannot leave two identities
// claiming one provider subject.
func TestBindSubjectMovesAnAlreadyBoundSubject(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	first := newTestIdentity(t, s, identity.IdentityID("30000000-0000-5000-8000-000000000003"), identity.KindBot, "first-agent")
	second := newTestIdentity(t, s, identity.IdentityID("30000000-0000-5000-8000-000000000004"), identity.KindBot, "second-agent")
	subject := identity.Subject{Issuer: "https://auth.catlow.cloud", Subject: "archie"}

	if err := s.BindSubject(ctx, first.ID, subject, bindAudit("bind:3")); err != nil {
		t.Fatalf("BindSubject() error = %v", err)
	}
	if err := s.BindSubject(ctx, second.ID, subject, bindAudit("bind:4")); err != nil {
		t.Fatalf("rebind error = %v", err)
	}

	got, err := s.ResolveSubject(ctx, subject)
	if err != nil {
		t.Fatalf("ResolveSubject() error = %v", err)
	}
	if got.ID != second.ID {
		t.Fatalf("ResolveSubject() = %s, want %s", got.ID, second.ID)
	}
}

func TestBindSubjectRequiresAuditAndAKnownIdentity(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	subject := identity.Subject{Issuer: "https://auth.catlow.cloud", Subject: "archie"}

	if err := s.BindSubject(ctx, identity.SystemID, subject, identity.Audit{}); err == nil {
		t.Fatal("BindSubject() accepted a binding with no audit")
	}
	if err := s.BindSubject(ctx, identity.IdentityID("does-not-exist"), subject, bindAudit("bind:5")); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("BindSubject() error = %v, want %v", err, identity.ErrNotFound)
	}
	if err := s.BindSubject(ctx, identity.SystemID, identity.Subject{}, bindAudit("bind:6")); err == nil {
		t.Fatal("BindSubject() accepted an incomplete subject")
	}
}

// TestEventAttributionSurvivesTheRoundTrip: the attributions are only useful if
// they come back out of the store, so a reader can answer who acted and whose
// authority they used.
func TestEventAttributionSurvivesTheRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	inserted, err := s.InsertEvent(ctx, events.Event{
		Kind:        events.KindAgentApproved,
		TaskID:      11,
		ActorID:     "30000000-0000-5000-8000-000000000001",
		ActorKind:   string(identity.KindBot),
		PrincipalID: "30000000-0000-5000-8000-000000000009",
		Detail:      "approved by an agent",
	})
	if err != nil {
		t.Fatalf("InsertEvent() error = %v", err)
	}

	got, err := s.TaskEvents(ctx, 11)
	if err != nil {
		t.Fatalf("TaskEvents() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("TaskEvents() returned %d events, want 1", len(got))
	}
	e := got[0]
	if e.ID != inserted {
		t.Fatalf("event id = %d, want %d", e.ID, inserted)
	}
	if e.Kind != events.KindAgentApproved {
		t.Fatalf("kind = %q, want %q", e.Kind, events.KindAgentApproved)
	}
	if e.ActorID == "" || e.ActorKind != string(identity.KindBot) || e.PrincipalID == "" {
		t.Fatalf("attribution did not survive the round trip: %+v", e)
	}
}
