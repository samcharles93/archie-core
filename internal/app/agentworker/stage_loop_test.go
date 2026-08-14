package agentworker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/eventbus"
)

type stageFetch struct {
	message agentexec.StageMessage
	err     error
	before  func()
}

type stageBusStub struct {
	fetches    []stageFetch
	fetchCalls int
}

func (b *stageBusStub) FetchStage(context.Context) (agentexec.StageMessage, error) {
	if b.fetchCalls >= len(b.fetches) {
		panic("unexpected FetchStage call")
	}
	fetch := b.fetches[b.fetchCalls]
	b.fetchCalls++
	if fetch.before != nil {
		fetch.before()
	}
	return fetch.message, fetch.err
}

type stageMessageStub struct {
	request       agentexec.AgentRequestMessage
	requestErr    error
	respondErr    error
	ackErr        error
	nakErr        error
	respondCancel context.CancelFunc
	respondCalls  int
	ackCalls      int
	nakCalls      int
}

func (m *stageMessageStub) Request() (agentexec.AgentRequestMessage, error) {
	return m.request, m.requestErr
}

func (m *stageMessageStub) Respond(context.Context, *agentexec.AgentResponseEnvelope) error {
	m.respondCalls++
	if m.respondCancel != nil {
		m.respondCancel()
	}
	return m.respondErr
}

func (m *stageMessageStub) Ack() error         { m.ackCalls++; return m.ackErr }
func (m *stageMessageStub) Nak() error         { m.nakCalls++; return m.nakErr }
func (*stageMessageStub) LogAttributes() []any { return []any{"reply_to", "_INBOX.test"} }

func stageLoopLogger() (*slog.Logger, *bytes.Buffer) {
	var output bytes.Buffer
	return slog.New(slog.NewTextHandler(&output, nil)), &output
}

func noDelay(t *testing.T) (func(time.Duration), *[]time.Duration) {
	t.Helper()
	var calls []time.Duration
	return func(duration time.Duration) { calls = append(calls, duration) }, &calls
}

func TestRunMainLoopExitsImmediatelyWhenAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	bus := &stageBusStub{}
	log, output := stageLoopLogger()
	if code := runMainLoop(ctx, bus, log); code != 0 {
		t.Fatalf("runMainLoop() = %d, want 0", code)
	}
	if bus.fetchCalls != 0 || !strings.Contains(output.String(), "archie-agent shutting down") {
		t.Fatalf("fetches/log = (%d, %q), want no fetch and shutdown log", bus.fetchCalls, output.String())
	}
}

func TestRunMainLoopFetchOutcomes(t *testing.T) {
	fetchErr := errors.New("temporary fetch failure")
	tests := []struct {
		name      string
		firstErr  error
		wantDelay bool
		wantLog   bool
	}{
		{name: "idle", firstErr: eventbus.ErrNoMessage},
		{name: "failure", firstErr: fetchErr, wantDelay: true, wantLog: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			bus := &stageBusStub{fetches: []stageFetch{
				{err: test.firstErr},
				{err: errors.New("fetch after cancellation"), before: cancel},
			}}
			log, output := stageLoopLogger()
			delay, delays := noDelay(t)
			if code := runMainLoopWithDelay(ctx, bus, log, delay); code != 0 {
				t.Fatalf("runMainLoopWithDelay() = %d, want 0", code)
			}
			if got := len(*delays) == 1 && (*delays)[0] == time.Second; got != test.wantDelay {
				t.Fatalf("delayed = %v (%v), want %v", got, *delays, test.wantDelay)
			}
			if got := strings.Contains(output.String(), "fetch failed"); got != test.wantLog {
				t.Fatalf("fetch failure logged = %v, want %v; %q", got, test.wantLog, output.String())
			}
		})
	}
}

func TestRunMainLoopStopsWhenFetchCancelsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	bus := &stageBusStub{fetches: []stageFetch{{err: errors.New("interrupted"), before: cancel}}}
	log, output := stageLoopLogger()
	delay, delays := noDelay(t)
	if code := runMainLoopWithDelay(ctx, bus, log, delay); code != 0 {
		t.Fatalf("runMainLoopWithDelay() = %d, want 0", code)
	}
	if len(*delays) != 0 || output.Len() != 0 {
		t.Fatalf("delay/log = (%v, %q), want none", *delays, output.String())
	}
}

func TestRunMainLoopNaksHandleErrorsAndContinues(t *testing.T) {
	for _, test := range []struct {
		name       string
		nakErr     error
		wantNakLog bool
	}{
		{name: "nak succeeds"},
		{name: "nak fails", nakErr: errors.New("nak unavailable"), wantNakLog: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			message := &stageMessageStub{requestErr: errors.New("decode request: invalid"), nakErr: test.nakErr}
			bus := &stageBusStub{fetches: []stageFetch{{message: message}, {err: errors.New("cancelled"), before: cancel}}}
			log, output := stageLoopLogger()
			delay, _ := noDelay(t)
			runMainLoopWithDelay(ctx, bus, log, delay)
			if message.nakCalls != 1 || message.ackCalls != 0 {
				t.Fatalf("nak/ack = (%d,%d), want (1,0)", message.nakCalls, message.ackCalls)
			}
			if got := strings.Contains(output.String(), "nak failed"); got != test.wantNakLog {
				t.Fatalf("nak failure logged = %v, want %v", got, test.wantNakLog)
			}
		})
	}
}

func TestRunMainLoopAcksSuccessfulHandleAndContinues(t *testing.T) {
	for _, test := range []struct {
		name       string
		ackErr     error
		wantAckLog bool
	}{
		{name: "ack succeeds"},
		{name: "ack fails", ackErr: errors.New("ack unavailable"), wantAckLog: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			message := &stageMessageStub{
				request:       agentexec.AgentRequestMessage{Providers: map[string]agentexec.Provider{"test": {Class: "openai"}}},
				ackErr:        test.ackErr,
				respondCancel: cancel,
			}
			bus := &stageBusStub{fetches: []stageFetch{{message: message}}}
			log, output := stageLoopLogger()
			delay, _ := noDelay(t)
			runMainLoopWithDelay(ctx, bus, log, delay)
			if message.respondCalls != 1 || message.ackCalls != 1 || message.nakCalls != 0 {
				t.Fatalf("respond/ack/nak = (%d,%d,%d), want (1,1,0)", message.respondCalls, message.ackCalls, message.nakCalls)
			}
			if got := strings.Contains(output.String(), "ack failed"); got != test.wantAckLog {
				t.Fatalf("ack failure logged = %v, want %v", got, test.wantAckLog)
			}
		})
	}
}
