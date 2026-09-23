package daemon

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/eventbus"
)

// ---- fakes ----

type fakeReactionSource struct {
	mu       sync.Mutex
	pending  []eventbus.Message
	fetchErr error
}

func (f *fakeReactionSource) Fetch(ctx context.Context) (eventbus.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	if len(f.pending) == 0 {
		return nil, eventbus.ErrNoMessage
	}
	m := f.pending[0]
	f.pending = f.pending[1:]
	return m, nil
}

type fakeReactionMessage struct {
	data    []byte
	subject string
	mu      sync.Mutex
	acked   bool
	nakked  bool
}

func (m *fakeReactionMessage) Data() []byte    { return m.data }
func (m *fakeReactionMessage) Subject() string { return m.subject }
func (m *fakeReactionMessage) Ack() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acked = true
	return nil
}

func (m *fakeReactionMessage) Nak() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nakked = true
	return nil
}

func (m *fakeReactionMessage) wasAcked() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.acked
}

func (m *fakeReactionMessage) wasNakked() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nakked
}

type fakeRemediations struct {
	begun    []string // payloads handed to BeginRemediation, in order
	updated  []string
	beginErr error
}

func (f *fakeRemediations) BeginRemediation(ctx context.Context, taskID int64, payload string) error {
	if f.beginErr != nil {
		return f.beginErr
	}
	f.begun = append(f.begun, payload)
	return nil
}

func (f *fakeRemediations) UpdateReviewPayload(ctx context.Context, taskID int64, payload string) error {
	f.updated = append(f.updated, payload)
	return nil
}

func (f *fakeRemediations) SetReviewCursors(ctx context.Context, taskID, reviewCursor, commentCursor int64) error {
	return nil
}

type fakeReviewLookup struct {
	task *workflowtask.Task
	err  error
	// sequence, when set, is consulted before task, so a test can model the
	// store's fresh read per message (e.g. a unit that queued between two
	// reactions).
	sequence func(call int) (*workflowtask.Task, error)
	calls    int
}

func (f *fakeReviewLookup) OpenTaskByPR(ctx context.Context, owner, repo string, number int) (*workflowtask.Task, error) {
	f.calls++
	if f.sequence != nil {
		return f.sequence(f.calls)
	}
	return f.task, f.err
}

// ---- helpers ----

func reviewEnvelopeJSON(kind string, reviewID, commentID int64, author, state, body string) []byte {
	e := workintake.ReviewCommentEnvelope{
		Owner: "acme", Repo: "widgets", PRNumber: 42,
		Kind:     workintake.ReviewReactionKind(kind),
		ReviewID: reviewID, CommentID: commentID,
		Author: author, State: state, Body: body,
	}
	data, err := e.Encode()
	if err != nil {
		panic(err)
	}
	return data
}

func ownedTask(status, workflowName, payload string) *workflowtask.Task {
	t := &workflowtask.Task{
		ID: 7, Owner: "acme", Repo: "widgets", PRNumber: 42,
		Status: status, Workflow: workflowName, ReviewPayload: payload,
	}
	return t
}

func newConsumer(task *workflowtask.Task, remediations *fakeRemediations) *reactionConsumer {
	return newReactionConsumer(
		&fakeReviewLookup{task: task},
		remediations,
		func(*workflowtask.Task) string { return "archie-bot" },
		slog.New(slog.DiscardHandler),
	)
}

// ---- tests ----

func TestReactionConsumerReviewEventStartsARemediation(t *testing.T) {
	remediations := &fakeRemediations{}
	c := newConsumer(ownedTask(workflowtask.StatusPROpen, "implement", ""), remediations)
	msg := &fakeReactionMessage{data: reviewEnvelopeJSON("review", 7, 0, "alice", "requested_changes", "please fix x")}

	c.handle(context.Background(), msg)

	if len(remediations.begun) != 1 {
		t.Fatalf("BeginRemediation calls = %d, want 1", len(remediations.begun))
	}
	unit, err := workflow.DecodeReviewUnit(remediations.begun[0])
	if err != nil {
		t.Fatal(err)
	}
	if unit.ReviewID != 7 || unit.State != "requested_changes" || unit.Body != "please fix x" {
		t.Errorf("unit = %+v, want the review's own coordinates", unit)
	}
	if !msg.wasAcked() {
		t.Error("message was not acked")
	}
}

func TestReactionConsumerApprovedReviewDoesNotRemediate(t *testing.T) {
	remediations := &fakeRemediations{}
	c := newConsumer(ownedTask(workflowtask.StatusPROpen, "implement", ""), remediations)
	msg := &fakeReactionMessage{data: reviewEnvelopeJSON("review", 7, 0, "alice", "approved", "lgtm")}

	c.handle(context.Background(), msg)

	if len(remediations.begun) != 0 {
		t.Errorf("BeginRemediation called for an approved review")
	}
	if !msg.wasAcked() {
		t.Error("message was not acked")
	}
}

func TestReactionConsumerOwnCommentNeverRetriggers(t *testing.T) {
	remediations := &fakeRemediations{}
	c := newConsumer(ownedTask(workflowtask.StatusPROpen, "implement", ""), remediations)
	msg := &fakeReactionMessage{data: reviewEnvelopeJSON("comment", 0, 9, "archie-bot", "", "my own reply")}

	c.handle(context.Background(), msg)

	if len(remediations.begun) != 0 || len(remediations.updated) != 0 {
		t.Errorf("own comment triggered work: begun=%d updated=%d", len(remediations.begun), len(remediations.updated))
	}
	if !msg.wasAcked() {
		t.Error("message was not acked")
	}
}

func TestReactionConsumerCommentAppendsToThePendingUnitOfItsReview(t *testing.T) {
	remediations := &fakeRemediations{}
	pending := ownedTask(workflowtask.StatusQueued, "remediate", `{"review_id":7,"state":"requested_changes"}`)
	c := newConsumer(pending, remediations)
	msg := &fakeReactionMessage{data: reviewEnvelopeJSON("comment", 7, 9, "alice", "", "fix the loop")}

	c.handle(context.Background(), msg)

	if len(remediations.updated) != 1 {
		t.Fatalf("UpdateReviewPayload calls = %d, want 1", len(remediations.updated))
	}
	unit, err := workflow.DecodeReviewUnit(remediations.updated[0])
	if err != nil {
		t.Fatal(err)
	}
	if unit.ReviewID != 7 || len(unit.Comments) != 1 || unit.Comments[0].CommentID != 9 {
		t.Errorf("appended unit = %+v, want comment 9 under review 7", unit)
	}
	// The parent review's summary survives the append.
	if unit.State != "requested_changes" {
		t.Errorf("unit state = %q, want the parent review's state preserved", unit.State)
	}
	if !msg.wasAcked() {
		t.Error("message was not acked")
	}
}

func TestReactionConsumerDuplicateCommentIsNotAppendedTwice(t *testing.T) {
	remediations := &fakeRemediations{}
	pending := ownedTask(workflowtask.StatusQueued, "remediate", `{"review_id":7,"comments":[{"comment_id":9,"body":"fix the loop"}]}`)
	c := newConsumer(pending, remediations)
	msg := &fakeReactionMessage{data: reviewEnvelopeJSON("comment", 7, 9, "alice", "", "fix the loop")}

	c.handle(context.Background(), msg)

	if len(remediations.updated) != 0 {
		t.Errorf("UpdateReviewPayload called for a comment the unit already carries")
	}
	if !msg.wasAcked() {
		t.Error("message was not acked")
	}
}

func TestReactionConsumerLateCommentAfterDispatchIsDropped(t *testing.T) {
	remediations := &fakeRemediations{}
	c := newConsumer(ownedTask(workflowtask.StatusPROpen, "implement", ""), remediations)
	// A comment carrying a parent review id, but the task is not a queued
	// remediation for it: the unit already dispatched.
	msg := &fakeReactionMessage{data: reviewEnvelopeJSON("comment", 7, 9, "alice", "", "late comment")}

	c.handle(context.Background(), msg)

	if len(remediations.begun) != 0 || len(remediations.updated) != 0 {
		t.Errorf("late comment mutated store state: begun=%d updated=%d", len(remediations.begun), len(remediations.updated))
	}
	if !msg.wasAcked() {
		t.Error("message was not acked")
	}
}

func TestReactionConsumerStandaloneCommentIsItsOwnUnit(t *testing.T) {
	remediations := &fakeRemediations{}
	c := newConsumer(ownedTask(workflowtask.StatusPROpen, "implement", ""), remediations)
	msg := &fakeReactionMessage{data: reviewEnvelopeJSON("comment", 0, 12, "alice", "", "nit: rename")}

	c.handle(context.Background(), msg)

	if len(remediations.begun) != 1 {
		t.Fatalf("BeginRemediation calls = %d, want 1", len(remediations.begun))
	}
	unit, err := workflow.DecodeReviewUnit(remediations.begun[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(unit.Comments) != 1 || unit.Comments[0].CommentID != 12 {
		t.Errorf("unit = %+v, want one standalone comment", unit)
	}
	if !msg.wasAcked() {
		t.Error("message was not acked")
	}
}

func TestReactionConsumerUnresolvablePRIsDropped(t *testing.T) {
	remediations := &fakeRemediations{}
	// nil task: the PR does not belong to an archie-owned task.
	c := newConsumer(nil, remediations)
	msg := &fakeReactionMessage{data: reviewEnvelopeJSON("review", 7, 0, "alice", "requested_changes", "unrelated")}

	c.handle(context.Background(), msg)

	if len(remediations.begun) != 0 {
		t.Errorf("reaction for a foreign PR started work")
	}
	if !msg.wasAcked() {
		t.Error("message was not acked")
	}
}

func TestReactionConsumerInvalidEnvelopeIsAckedNotRedelivered(t *testing.T) {
	remediations := &fakeRemediations{}
	c := newConsumer(ownedTask(workflowtask.StatusPROpen, "implement", ""), remediations)
	msg := &fakeReactionMessage{data: []byte(`{"owner":"acme"}`)} // no kind, no ids

	c.handle(context.Background(), msg)

	if len(remediations.begun) != 0 {
		t.Errorf("invalid envelope started work")
	}
	if !msg.wasAcked() {
		t.Error("message was not acked")
	}
}

func TestReactionConsumerTransientStoreErrorNaks(t *testing.T) {
	remediations := &fakeRemediations{beginErr: errors.New("db unavailable")}
	c := newConsumer(ownedTask(workflowtask.StatusPROpen, "implement", ""), remediations)
	msg := &fakeReactionMessage{data: reviewEnvelopeJSON("review", 7, 0, "alice", "requested_changes", "please fix x")}

	c.handle(context.Background(), msg)

	if !msg.wasNakked() {
		t.Error("transient failure was not nakked for redelivery")
	}
	if msg.wasAcked() {
		t.Error("transient failure was acked")
	}
}

func TestReactionConsumerDrainsUntilIdle(t *testing.T) {
	remediations := &fakeRemediations{}
	// The lookup reads the row fresh per message: the review event queues the
	// remediation, so the comment event resolves against the queued unit.
	lookup := &fakeReviewLookup{sequence: func(call int) (*workflowtask.Task, error) {
		if call == 1 {
			return ownedTask(workflowtask.StatusPROpen, "implement", ""), nil
		}
		return ownedTask(workflowtask.StatusQueued, "remediate", `{"review_id":7,"state":"requested_changes","body":"one"}`), nil
	}}
	c := newReactionConsumer(lookup, remediations, func(*workflowtask.Task) string { return "archie-bot" }, slog.New(slog.DiscardHandler))
	src := &fakeReactionSource{pending: []eventbus.Message{
		&fakeReactionMessage{data: reviewEnvelopeJSON("review", 7, 0, "alice", "requested_changes", "one")},
		&fakeReactionMessage{data: reviewEnvelopeJSON("comment", 7, 8, "alice", "", "one-a")},
	}}

	n, err := c.drain(context.Background(), src, 8)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("handled = %d, want 2", n)
	}
	// The review queued the unit; the comment appended to it.
	if len(remediations.begun) != 1 || len(remediations.updated) != 1 {
		t.Errorf("begun=%d updated=%d, want 1/1", len(remediations.begun), len(remediations.updated))
	}
	unit, err := workflow.DecodeReviewUnit(remediations.updated[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(unit.Comments) != 1 || unit.Comments[0].CommentID != 8 {
		t.Errorf("appended unit = %+v, want comment 8 collected under review 7", unit)
	}
}

// compile-time interface checks
var (
	_ workintake.ReviewTaskLookup      = (*fakeReviewLookup)(nil)
	_ storecontract.RemediationStarter = (*fakeRemediations)(nil)
)
