package channels

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// recordingDeleter is a sender that can retract messages. report controls the
// Delete capability it advertises, so a test can prove the caller trusts the
// capability rather than the method set.
type recordingDeleter struct {
	report bool

	mu      sync.Mutex
	deleted []string
	err     error
}

func (d *recordingDeleter) DeleteMessage(_ context.Context, event messaging.MessageEvent) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deleted = append(d.deleted, event.ID)
	return d.err
}

func (d *recordingDeleter) Capabilities() messaging.AdapterCapabilities {
	return messaging.AdapterCapabilities{Delete: d.report}
}

func (d *recordingDeleter) deletions() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.deleted...)
}

// plainSender is a launch-scoped sender that offers no optional delivery
// behavior at all -- the shape every adapter that cannot delete has.
type plainSender struct{}

// syncBuffer is a bytes.Buffer safe for a log record written on a retraction
// goroutine while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newDebugLogger(buf *syncBuffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// sentEvent returns a send func that reports the platform's delivered event,
// carrying id, so a retraction has something to address.
func sentEvent(id string) SendFunc {
	return func(_ context.Context, event messaging.MessageEvent) (messaging.MessageEvent, error) {
		event.ID = id
		return event, nil
	}
}

// An ephemeral reply must be retracted once its TTL elapses, not before, and
// the retraction must address the message the send actually delivered. The
// clock is injected, so the test proves the TTL ordering without sleeping.
func TestEphemeralSenderRetractsAfterTTL(t *testing.T) {
	deleter := &recordingDeleter{report: true}
	fire := make(chan time.Time)
	var gotTTL time.Duration

	sender := NewEphemeralSender(slog.New(slog.DiscardHandler), func(d time.Duration) <-chan time.Time {
		gotTTL = d
		return fire
	})

	sent, err := sender.Send(t.Context(), deleter, messaging.EphemeralReply{
		Event: messaging.MessageEvent{Type: messaging.MsgText, Text: "running tool X…"},
		TTL:   5 * time.Second,
	}, sentEvent("42"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sent.ID != "42" {
		t.Fatalf("Send returned ID %q, want the delivered id", sent.ID)
	}
	if got := deleter.deletions(); len(got) != 0 {
		t.Fatalf("retracted %v before the TTL elapsed, want nothing yet", got)
	}

	fire <- time.Now()
	sender.Wait()

	got := deleter.deletions()
	if len(got) != 1 || got[0] != "42" {
		t.Fatalf("retracted %v, want exactly the delivered message 42", got)
	}
	if gotTTL != 5*time.Second {
		t.Fatalf("scheduled retraction after %v, want the reply's TTL 5s", gotTTL)
	}
}

// A platform with no delete support must still deliver the reply: the
// message is sent, left in place, and the capability gap is logged, never
// returned as a turn error.
func TestEphemeralSenderDegradesWhenSenderCannotDelete(t *testing.T) {
	var buf syncBuffer
	sender := NewEphemeralSender(newDebugLogger(&buf), func(time.Duration) <-chan time.Time {
		t.Error("scheduled a retraction for a sender with no delete support")
		return closedTimeChan()
	})

	sendCalled := false
	_, err := sender.Send(t.Context(), &plainSender{}, messaging.EphemeralReply{
		Event: messaging.MessageEvent{Type: messaging.MsgText, Text: "thinking…"},
		TTL:   time.Second,
	}, func(_ context.Context, event messaging.MessageEvent) (messaging.MessageEvent, error) {
		sendCalled = true
		event.ID = "7"
		return event, nil
	})
	if err != nil {
		t.Fatalf("Send returned an error for a sender that cannot delete: %v", err)
	}
	if !sendCalled {
		t.Fatal("the reply was not sent; it must degrade to a normal send")
	}

	sender.Wait()
	if !strings.Contains(buf.String(), "cannot retract") {
		t.Fatalf("log = %q, want it to record the missed retraction", buf.String())
	}
}

// The capability is the contract, not the method set: an adapter that
// implements DeleteMessage but reports Delete:false must not be retracted.
func TestEphemeralSenderTrustsCapabilityOverMethodSet(t *testing.T) {
	deleter := &recordingDeleter{report: false}
	fire := make(chan time.Time)
	close(fire) // fire at once if anything schedules a retraction

	sender := NewEphemeralSender(slog.New(slog.DiscardHandler), func(time.Duration) <-chan time.Time {
		return fire
	})

	if _, err := sender.Send(t.Context(), deleter, messaging.EphemeralReply{
		Event: messaging.MessageEvent{Type: messaging.MsgText, Text: "thinking…"},
		TTL:   time.Second,
	}, sentEvent("9")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sender.Wait()

	if got := deleter.deletions(); len(got) != 0 {
		t.Fatalf("retracted %v despite Capabilities().Delete == false", got)
	}
}

// A failed send has no message to retract, and its error belongs to the
// caller: Send returns it unchanged and schedules nothing.
func TestEphemeralSenderPropagatesSendError(t *testing.T) {
	wantErr := errors.New("send refused")
	deleter := &recordingDeleter{report: true}
	sender := NewEphemeralSender(slog.New(slog.DiscardHandler), func(time.Duration) <-chan time.Time {
		t.Error("scheduled a retraction for a message that was never sent")
		return closedTimeChan()
	})

	_, err := sender.Send(t.Context(), deleter, messaging.EphemeralReply{
		Event: messaging.MessageEvent{Type: messaging.MsgText, Text: "thinking…"},
		TTL:   time.Second,
	}, func(context.Context, messaging.MessageEvent) (messaging.MessageEvent, error) {
		return messaging.MessageEvent{}, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Send error = %v, want %v", err, wantErr)
	}
	sender.Wait()
	if got := deleter.deletions(); len(got) != 0 {
		t.Fatalf("retracted %v after a failed send", got)
	}
}

// A reply with no lifetime and one whose send reported no message id both
// schedule nothing: there is either no deadline or no message to address.
func TestEphemeralSenderSchedulesNothingWithoutTTLOrMessageID(t *testing.T) {
	closed := closedTimeChan()
	tests := []struct {
		name string
		ttl  time.Duration
		id   string
	}{
		{name: "zero TTL", ttl: 0, id: "11"},
		{name: "negative TTL", ttl: -time.Second, id: "11"},
		{name: "no message id", ttl: time.Second, id: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deleter := &recordingDeleter{report: true}
			sender := NewEphemeralSender(slog.New(slog.DiscardHandler), func(time.Duration) <-chan time.Time {
				return closed
			})
			if _, err := sender.Send(t.Context(), deleter, messaging.EphemeralReply{
				Event: messaging.MessageEvent{Type: messaging.MsgText, Text: "thinking…"},
				TTL:   tt.ttl,
			}, sentEvent(tt.id)); err != nil {
				t.Fatalf("Send: %v", err)
			}
			sender.Wait()
			if got := deleter.deletions(); len(got) != 0 {
				t.Fatalf("retracted %v, want nothing scheduled", got)
			}
		})
	}
}

// A retraction failure is logged, not returned: the reply was delivered, and
// a platform hiccup removing it later must not surface as a turn error.
func TestEphemeralSenderLogsRetractionFailure(t *testing.T) {
	var buf syncBuffer
	deleter := &recordingDeleter{report: true, err: errors.New("message not found")}
	fire := make(chan time.Time)
	close(fire)

	sender := NewEphemeralSender(newDebugLogger(&buf), func(time.Duration) <-chan time.Time {
		return fire
	})
	if _, err := sender.Send(t.Context(), deleter, messaging.EphemeralReply{
		Event: messaging.MessageEvent{Type: messaging.MsgText, Text: "thinking…"},
		TTL:   time.Second,
	}, sentEvent("13")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sender.Wait()

	if !strings.Contains(buf.String(), "retraction failed") {
		t.Fatalf("log = %q, want it to record the failed retraction", buf.String())
	}
}

func closedTimeChan() <-chan time.Time {
	ch := make(chan time.Time)
	close(ch)
	return ch
}
