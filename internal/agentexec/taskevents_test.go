package agentexec

import (
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
)

var errPublishBoom = errors.New("publish boom")

// eventPublisherStub is an EventPublisher that records every call and can be
// told to fail, mirroring fakePublisher in systemlog_test.go for the same
// reason: assert both the happy path and "one failure must not stop the
// rest."
type eventPublisherStub struct {
	subjects []string
	payloads [][]byte
	failWith error
}

func (p *eventPublisherStub) Publish(subject string, data []byte) error {
	p.subjects = append(p.subjects, subject)
	p.payloads = append(p.payloads, append([]byte(nil), data...))
	return p.failWith
}

// TestForwardTaskEventsPublishesEachEventOnItsTaskSubject is the core
// contract: everything published on the bus while ForwardTaskEvents is
// running arrives at pub, JSON-decodable back into the same Event, on
// SubjectForEvents(taskID) -- regardless of which task's events happen to be
// flowing, since taskID is a ForwardTaskEvents parameter, not read from the
// event itself.
func TestForwardTaskEventsPublishesEachEventOnItsTaskSubject(t *testing.T) {
	bus := events.NewBus()
	sub := bus.Subscribe(8)
	pub := &eventPublisherStub{}

	done := make(chan struct{})
	go func() {
		ForwardTaskEvents(sub, pub, 42, slog.New(slog.DiscardHandler))
		close(done)
	}()

	want := []events.Event{
		{Kind: events.KindStageStart, TaskID: 42, Stage: "baseline"},
		{Kind: events.KindStageFinish, TaskID: 42, Stage: "baseline", Data: map[string]any{"duration_ms": float64(120)}},
		{Kind: events.KindParked, TaskID: 42, Detail: "baseline red"},
	}
	for _, e := range want {
		bus.Publish(e)
	}
	sub.Close()
	<-done

	if len(pub.payloads) != len(want) {
		t.Fatalf("published %d events, want %d", len(pub.payloads), len(want))
	}
	wantSubject := SubjectForEvents(42)
	for i, payload := range pub.payloads {
		if pub.subjects[i] != wantSubject {
			t.Errorf("event %d subject = %q, want %q", i, pub.subjects[i], wantSubject)
		}
		var got events.Event
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatalf("event %d undecodable: %v", i, err)
		}
		if got.Kind != want[i].Kind || got.TaskID != want[i].TaskID || got.Stage != want[i].Stage || got.Detail != want[i].Detail {
			t.Errorf("event %d = %+v, want %+v", i, got, want[i])
		}
		if got.At.IsZero() {
			t.Errorf("event %d has zero At, want the bus-stamped publish time", i)
		}
	}
}

// TestForwardTaskEventsSurvivesAPublishFailure pins "one missing event must
// never abort the ones after it": event shipping must never block or fail
// the workflow run it is reporting on.
func TestForwardTaskEventsSurvivesAPublishFailure(t *testing.T) {
	bus := events.NewBus()
	sub := bus.Subscribe(8)
	pub := &eventPublisherStub{failWith: errPublishBoom}

	done := make(chan struct{})
	go func() {
		ForwardTaskEvents(sub, pub, 7, slog.New(slog.DiscardHandler))
		close(done)
	}()

	bus.Publish(events.Event{Kind: events.KindStageStart, TaskID: 7})
	bus.Publish(events.Event{Kind: events.KindOutcome, TaskID: 7})
	sub.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ForwardTaskEvents did not return after sub.Close(); a publish failure blocked the loop")
	}

	if len(pub.payloads) != 2 {
		t.Fatalf("published %d events despite failures, want 2 (both attempted)", len(pub.payloads))
	}
}
