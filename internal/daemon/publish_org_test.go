package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/eventbus"
	"github.com/samcharles93/archie-core/internal/forge"
)

// captureBus is a TaskBus that records the one published envelope, so a test
// can assert the org the producer resolved before encoding.
type captureBus struct {
	subject  string
	key      string
	payload  []byte
	publish  int
	pubErr   error
	fetchErr error
}

func (b *captureBus) PublishUnique(_ context.Context, subject, key string, payload []byte) error {
	if b.pubErr != nil {
		return b.pubErr
	}
	b.subject, b.key, b.payload = subject, key, payload
	b.publish++
	return nil
}

func (b *captureBus) Fetch(context.Context) (eventbus.Message, error) {
	if b.fetchErr != nil {
		return nil, b.fetchErr
	}
	return nil, eventbus.ErrNoMessage
}

func (b *captureBus) Request(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("captureBus: Request is not part of this test")
}

// stubPrincipals is the access.PrincipalSource the producer resolves against,
// keyed by identity so a test can make each identity serve a distinct org.
type stubPrincipals struct {
	orgs map[identity.IdentityID]org.OrgID
	err  error
}

func (s stubPrincipals) PrincipalFor(_ context.Context, id identity.IdentityID) (access.Principal, error) {
	if s.err != nil {
		return access.Principal{}, s.err
	}
	return access.Principal{IdentityID: id, Org: s.orgs[id]}, nil
}

func decodedTask(t *testing.T, payload []byte) workintake.TaskEnvelope {
	t.Helper()
	var got workintake.TaskEnvelope
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode published task: %v", err)
	}
	return got
}

// TestPublishTaskStampsTheResolvedIdentityOrg is the core scbe assertion: the
// poller's envelope must carry the org the identity serves, not the default,
// so the idempotency key is org/identity/owner/repo/number for a multi-org
// install (docs/prds/orgs-and-access.md, "Events and task identity").
func TestPublishTaskStampsTheResolvedIdentityOrg(t *testing.T) {
	id := identity.StableID("worker")
	bus := &captureBus{}
	d := &Daemon{
		Tasks:      bus,
		Log:        slog.New(slog.DiscardHandler),
		Identities: []*IdentityRunner{{ID: id, Name: "worker"}},
		Principals: stubPrincipals{orgs: map[identity.IdentityID]org.OrgID{id: "soc"}},
	}

	err := d.PublishTask(t.Context(), workintake.TaskEnvelope{
		Owner: "acme", Repo: "widget", Number: 1, Identity: string(id),
	})
	if err != nil {
		t.Fatalf("PublishTask: %v", err)
	}

	got := decodedTask(t, bus.payload)
	if got.Org != "soc" {
		t.Errorf("published org = %q, want the identity's org %q", got.Org, "soc")
	}
	if want := "soc/" + string(id) + "/acme/widget/1"; !strings.Contains(bus.key, want) {
		t.Errorf("idempotency key = %q, want it to contain %q", bus.key, want)
	}
}

// TestPublishTaskUsesTheRootOrgForASingleIdentityEnvelope pins the webhook
// producer's shape: an empty identity is the root identity of a
// single-operator install, so its org is the org the key must carry.
func TestPublishTaskUsesTheRootOrgForASingleIdentityEnvelope(t *testing.T) {
	root := identity.StableID("root")
	bus := &captureBus{}
	d := &Daemon{
		Tasks:          bus,
		Log:            slog.New(slog.DiscardHandler),
		RootIdentityID: root,
		Principals:     stubPrincipals{orgs: map[identity.IdentityID]org.OrgID{root: "soc"}},
	}

	if err := d.PublishTask(t.Context(), workintake.TaskEnvelope{
		Owner: "acme", Repo: "widget", Number: 2,
	}); err != nil {
		t.Fatalf("PublishTask: %v", err)
	}

	if got := decodedTask(t, bus.payload).Org; got != "soc" {
		t.Errorf("published org = %q, want the root identity's org %q", got, "soc")
	}
}

// TestPublishTaskFailsClosedWhenTheOrgCannotBeResolved: publishing under a
// guessed org would let the same issue key differently across deliveries, so
// an org-RPC failure must stop the publish rather than fall back to default.
func TestPublishTaskFailsClosedWhenTheOrgCannotBeResolved(t *testing.T) {
	id := identity.StableID("worker")
	bus := &captureBus{}
	d := &Daemon{
		Tasks:      bus,
		Log:        slog.New(slog.DiscardHandler),
		Identities: []*IdentityRunner{{ID: id, Name: "worker"}},
		Principals: stubPrincipals{err: errors.New("state store unavailable")},
	}

	err := d.PublishTask(t.Context(), workintake.TaskEnvelope{
		Owner: "acme", Repo: "widget", Number: 3, Identity: string(id),
	})
	if err == nil {
		t.Fatal("PublishTask succeeded with an unresolved org; it must fail closed")
	}
	if bus.publish != 0 {
		t.Fatalf("published %d envelope(s) after an org resolve failure, want 0", bus.publish)
	}
}

// TestPublishTaskDefaultsToTheDefaultOrgWithoutAPrincipalSource: an install
// with no State Store has no org surface, so the default org remains correct.
func TestPublishTaskDefaultsToTheDefaultOrgWithoutAPrincipalSource(t *testing.T) {
	bus := &captureBus{}
	d := &Daemon{Tasks: bus, Log: slog.New(slog.DiscardHandler)}

	if err := d.PublishTask(t.Context(), workintake.TaskEnvelope{
		Owner: "acme", Repo: "widget", Number: 4,
	}); err != nil {
		t.Fatalf("PublishTask: %v", err)
	}

	if got := decodedTask(t, bus.payload).Org; got != org.DefaultOrgID {
		t.Errorf("published org = %q, want %q", got, org.DefaultOrgID)
	}
}

// TestScanPRReviewReactionsCarriesTheTaskOrg: the review scan already holds
// the task, so the org it resolved for the PR is the org its reactions must
// key on -- no second lookup and no cross-org drift between the two producers.
func TestScanPRReviewReactionsCarriesTheTaskOrg(t *testing.T) {
	reader := &fakeReviewReader{
		reviews: []forge.Review{{ID: 7, Author: "alice", State: "requested_changes", Body: "fix"}},
	}
	publisher := &recordingPublisher{}
	task := openTask(42, 0, 0)
	task.Org = "soc"

	if _, _, err := scanPRReviewReactions(
		t.Context(), reader, task, "archie-bot", 0, 0, publisher.publish); err != nil {
		t.Fatal(err)
	}
	if len(publisher.reactions) != 1 {
		t.Fatalf("published = %d, want 1", len(publisher.reactions))
	}
	if got := publisher.reactions[0].Org; got != "soc" {
		t.Errorf("reaction org = %q, want the task's org %q", got, "soc")
	}
}

// TestPublishReactionStampsTheRootOrgWhenTheProducerCouldNot: the webhook
// producer has no owned task to read an org from, so the reaction write path
// resolves the root identity's org -- the same org the poll's scan stamps --
// or the two deliveries of one review key differently.
func TestPublishReactionStampsTheRootOrgWhenTheProducerCouldNot(t *testing.T) {
	root := identity.StableID("root")
	bus := &captureBus{}
	d := &Daemon{
		Tasks:          bus,
		Log:            slog.New(slog.DiscardHandler),
		RootIdentityID: root,
		Principals:     stubPrincipals{orgs: map[identity.IdentityID]org.OrgID{root: "soc"}},
	}

	err := d.PublishReaction(t.Context(), workintake.ReviewCommentEnvelope{
		Owner: "acme", Repo: "widget", PRNumber: 1,
		Kind: workintake.ReviewReactionReview, ReviewID: 5, Author: "alice",
	})
	if err != nil {
		t.Fatalf("PublishReaction: %v", err)
	}

	var got workintake.ReviewCommentEnvelope
	if err := json.Unmarshal(bus.payload, &got); err != nil {
		t.Fatalf("decode published reaction: %v", err)
	}
	if got.Org != "soc" {
		t.Errorf("published org = %q, want the root identity's org %q", got.Org, "soc")
	}
	if want := "review/soc/acme/widget/1/review/5"; !strings.Contains(bus.key, want) {
		t.Errorf("idempotency key = %q, want it to contain %q", bus.key, want)
	}
}

// TestPublishReactionKeepsTheScansResolvedOrg: a reaction the scan already
// resolved from its task must not be re-resolved to the root org, or a
// multi-identity install would collapse every org's reactions onto one key.
func TestPublishReactionKeepsTheScansResolvedOrg(t *testing.T) {
	bus := &captureBus{}
	d := &Daemon{Tasks: bus, Log: slog.New(slog.DiscardHandler)}

	err := d.PublishReaction(t.Context(), workintake.ReviewCommentEnvelope{
		Owner: "acme", Repo: "widget", PRNumber: 1,
		Kind: workintake.ReviewReactionReview, ReviewID: 5, Org: "soc",
	})
	if err != nil {
		t.Fatalf("PublishReaction: %v", err)
	}

	var got workintake.ReviewCommentEnvelope
	if err := json.Unmarshal(bus.payload, &got); err != nil {
		t.Fatalf("decode published reaction: %v", err)
	}
	if got.Org != "soc" {
		t.Errorf("published org = %q, want the scan's org %q", got.Org, "soc")
	}
}
