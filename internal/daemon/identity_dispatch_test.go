package daemon

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/eventbus"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// fakeMessage is a minimal eventbus.Message for driving processNATSTask
// without standing up a broker.
type fakeMessage struct {
	data    []byte
	subject string
	acked   bool
	naked   bool
}

func (m *fakeMessage) Data() []byte    { return m.data }
func (m *fakeMessage) Subject() string { return m.subject }
func (m *fakeMessage) Ack() error      { m.acked = true; return nil }
func (m *fakeMessage) Nak() error      { m.naked = true; return nil }

var _ eventbus.Message = (*fakeMessage)(nil)

// TestProcessNATSTaskRejectsUnresolvableIdentity pins the intake half of the
// fail-closed relation: a task envelope naming an identity this daemon does
// not know must not be written to the store at all. Today it is enqueued and
// only fails open later, when forgeFor/repoFor fall back to the root forge.
func TestProcessNATSTaskRejectsUnresolvableIdentity(t *testing.T) {
	s := pgstore.Open(t)
	d := &Daemon{
		Store: s,
		Cfg:   config.NewHolder(config.Config{}),
		Trees: &worktree.Manager{},
		Log:   slog.New(slog.DiscardHandler),
	}

	data, err := workintake.TaskEnvelope{
		Owner: "acme", Repo: "widget", Number: 1, Title: "ghost task",
		Identity: "ghost",
	}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	msg := &fakeMessage{data: data, subject: workintake.SubjectTaskDefault}

	d.processNATSTask(t.Context(), msg)

	if msg.naked {
		t.Fatal("unresolvable identity intake was nacked; a redelivery can never resolve it")
	}
	if !msg.acked {
		t.Fatal("unresolvable identity intake was not dropped (acked)")
	}
	task, err := s.TaskByIssue(t.Context(), "acme", "widget", 1)
	if err != nil {
		t.Fatal(err)
	}
	if task != nil {
		t.Fatalf("task %d was created with an unresolvable identity %q", task.ID, task.Identity)
	}
}

// TestProcessParksTaskWhoseIdentityNoLongerResolves pins the dispatch half of
// the fail-closed relation: a task whose non-empty identity no longer has a
// configured runner must park, never fall back to the root forge's credential.
func TestProcessParksTaskWhoseIdentityNoLongerResolves(t *testing.T) {
	d, s, rootFg, _, _ := twoIdentityDaemon(t)
	d.Log = slog.New(slog.DiscardHandler)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "shared", 42, "t", "b", "", "ghost"); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("ClaimNext = (%+v, %v)", task, err)
	}

	d.process(ctx, task)

	got, err := s.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != workflow.StatusParked {
		t.Fatalf("status = %q, want parked", got.Status)
	}
	if !strings.Contains(got.ParkReason, "no longer resolves") {
		t.Errorf("ParkReason = %q, want it to name the unresolvable identity", got.ParkReason)
	}
	// The root forge must not have been touched for this task: parking on an
	// identity that no longer resolves is the fail-closed outcome, not a
	// fallback to root credentials.
	if calls := len(rootFg.stateLabels) + len(rootFg.closedIssues) + len(rootFg.comments); calls != 0 {
		t.Errorf("root forge was called %d time(s) for a task with an unresolvable identity", calls)
	}
}

// TestProcessParksRetiredIdentity pins the lifecycle half: an identity that
// still has a runner but has been retired may not act, so its tasks park
// rather than running under a credential the control plane has retired.
func TestProcessParksRetiredIdentity(t *testing.T) {
	s := pgstore.Open(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)

	id := identity.StableID("worker")
	created, err := identity.New(id, identity.KindBot, "worker")
	if err != nil {
		t.Fatal(err)
	}
	created, err = s.Create(ctx, created, identity.Audit{ActorID: identity.SystemID, Source: "test", RequestID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(ctx, created.ID, created.Version, identity.Command{Type: identity.CommandRetire}, identity.Audit{ActorID: identity.SystemID, Source: "test", RequestID: "retire"}); err != nil {
		t.Fatal(err)
	}

	repo := config.Repo{Owner: "acme", Name: "widget"}
	d := &Daemon{
		Cfg:                config.NewHolder(config.Config{Repos: []config.Repo{repo}}),
		Store:              s,
		Log:                log,
		IdentityRepository: s,
		Identities: []*IdentityRunner{{
			Name: "worker", ID: id,
			Forge: &testForge{}, Trees: &worktree.Manager{},
			Repos: []config.Repo{repo}, Cfg: config.IdentityConfig{Name: "worker"}, Log: log,
		}},
	}

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 7, "t", "b", "", string(id)); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("ClaimNext = (%+v, %v)", task, err)
	}

	d.process(ctx, task)

	got, err := s.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != workflow.StatusParked {
		t.Fatalf("status = %q, want parked", got.Status)
	}
	if !strings.Contains(got.ParkReason, "may not act") {
		t.Errorf("ParkReason = %q, want it to name the retired identity", got.ParkReason)
	}
}
