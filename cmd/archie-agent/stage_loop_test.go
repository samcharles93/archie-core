package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/eventbus"
)

type stageFetch struct {
	message eventbus.Message
	err     error
	before  func()
}

type stageBusStub struct {
	fetches       []stageFetch
	fetchCalls    int
	respondCalls  int
	respondCancel context.CancelFunc
}

func (b *stageBusStub) Fetch(context.Context) (eventbus.Message, error) {
	if b.fetchCalls >= len(b.fetches) {
		panic("unexpected Fetch call")
	}
	fetch := b.fetches[b.fetchCalls]
	b.fetchCalls++
	if fetch.before != nil {
		fetch.before()
	}
	return fetch.message, fetch.err
}

func (b *stageBusStub) Respond(context.Context, string, []byte) error {
	b.respondCalls++
	if b.respondCancel != nil {
		b.respondCancel()
	}
	return nil
}

type stageMessageStub struct {
	data     []byte
	ackErr   error
	nakErr   error
	ackCalls int
	nakCalls int
}

func (m *stageMessageStub) Data() []byte { return m.data }

func (m *stageMessageStub) Subject() string { return "archie.agent.test" }

func (m *stageMessageStub) ReplyAddress() (string, error) { return "_INBOX.test", nil }

func (m *stageMessageStub) Ack() error {
	m.ackCalls++
	return m.ackErr
}

func (m *stageMessageStub) Nak() error {
	m.nakCalls++
	return m.nakErr
}

func stageLoopLogger() (*slog.Logger, *bytes.Buffer) {
	var output bytes.Buffer
	return slog.New(slog.NewTextHandler(&output, nil)), &output
}

func noDelay(t *testing.T) (func(time.Duration), *[]time.Duration) {
	t.Helper()
	var calls []time.Duration
	return func(duration time.Duration) {
		calls = append(calls, duration)
	}, &calls
}

func TestRunMainLoopExitsImmediatelyWhenAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	bus := &stageBusStub{}
	log, output := stageLoopLogger()

	if code := runMainLoop(ctx, bus, log); code != 0 {
		t.Fatalf("runMainLoop() = %d, want 0", code)
	}
	if bus.fetchCalls != 0 {
		t.Fatalf("Fetch calls = %d, want 0", bus.fetchCalls)
	}
	if !strings.Contains(output.String(), "archie-agent shutting down") {
		t.Fatalf("log output %q does not contain shutdown message", output.String())
	}
}

func TestRunMainLoopRetriesNoMessageWithoutDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	bus := &stageBusStub{fetches: []stageFetch{
		{err: eventbus.ErrNoMessage},
		{err: errors.New("fetch after cancellation"), before: cancel},
	}}
	log, output := stageLoopLogger()
	delay, delays := noDelay(t)

	if code := runMainLoopWithDelay(ctx, bus, log, delay); code != 0 {
		t.Fatalf("runMainLoopWithDelay() = %d, want 0", code)
	}
	if bus.fetchCalls != 2 {
		t.Fatalf("Fetch calls = %d, want 2", bus.fetchCalls)
	}
	if len(*delays) != 0 {
		t.Fatalf("delay calls = %v, want none", *delays)
	}
	if strings.Contains(output.String(), "fetch failed") {
		t.Fatalf("log output %q unexpectedly contains fetch failure", output.String())
	}
}

func TestRunMainLoopLogsShutdownWhenNoMessageFetchCancelsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	bus := &stageBusStub{fetches: []stageFetch{{err: eventbus.ErrNoMessage, before: cancel}}}
	log, output := stageLoopLogger()
	delay, delays := noDelay(t)

	if code := runMainLoopWithDelay(ctx, bus, log, delay); code != 0 {
		t.Fatalf("runMainLoopWithDelay() = %d, want 0", code)
	}
	if bus.fetchCalls != 1 {
		t.Fatalf("Fetch calls = %d, want 1", bus.fetchCalls)
	}
	if len(*delays) != 0 {
		t.Fatalf("delay calls = %v, want none", *delays)
	}
	if !strings.Contains(output.String(), "archie-agent shutting down") {
		t.Fatalf("log output %q does not contain shutdown message", output.String())
	}
}

func TestRunMainLoopLogsDelaysAndRetriesFetchError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	fetchErr := errors.New("temporary fetch failure")
	bus := &stageBusStub{fetches: []stageFetch{
		{err: fetchErr},
		{err: errors.New("fetch after cancellation"), before: cancel},
	}}
	log, output := stageLoopLogger()
	delay, delays := noDelay(t)

	if code := runMainLoopWithDelay(ctx, bus, log, delay); code != 0 {
		t.Fatalf("runMainLoopWithDelay() = %d, want 0", code)
	}
	if bus.fetchCalls != 2 {
		t.Fatalf("Fetch calls = %d, want 2", bus.fetchCalls)
	}
	if len(*delays) != 1 || (*delays)[0] != time.Second {
		t.Fatalf("delay calls = %v, want [%s]", *delays, time.Second)
	}
	if !strings.Contains(output.String(), "fetch failed") || !strings.Contains(output.String(), fetchErr.Error()) {
		t.Fatalf("log output %q does not contain fetch failure", output.String())
	}
}

func TestRunMainLoopStopsAfterDelayWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	fetchErr := errors.New("temporary fetch failure")
	bus := &stageBusStub{fetches: []stageFetch{{err: fetchErr}}}
	log, output := stageLoopLogger()
	var delays []time.Duration
	delay := func(duration time.Duration) {
		delays = append(delays, duration)
		cancel()
	}

	if code := runMainLoopWithDelay(ctx, bus, log, delay); code != 0 {
		t.Fatalf("runMainLoopWithDelay() = %d, want 0", code)
	}
	if bus.fetchCalls != 1 {
		t.Fatalf("Fetch calls = %d, want 1", bus.fetchCalls)
	}
	if len(delays) != 1 || delays[0] != time.Second {
		t.Fatalf("delay calls = %v, want [%s]", delays, time.Second)
	}
	if !strings.Contains(output.String(), "fetch failed") || !strings.Contains(output.String(), "archie-agent shutting down") {
		t.Fatalf("log output %q does not contain fetch failure and shutdown messages", output.String())
	}
}

func TestRunMainLoopDoesNotLogOrDelayFetchErrorAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	bus := &stageBusStub{fetches: []stageFetch{{
		err:    errors.New("fetch interrupted"),
		before: cancel,
	}}}
	log, output := stageLoopLogger()
	delay, delays := noDelay(t)

	if code := runMainLoopWithDelay(ctx, bus, log, delay); code != 0 {
		t.Fatalf("runMainLoopWithDelay() = %d, want 0", code)
	}
	if len(*delays) != 0 {
		t.Fatalf("delay calls = %v, want none", *delays)
	}
	if output.Len() != 0 {
		t.Fatalf("log output = %q, want empty", output.String())
	}
}

func TestRunMainLoopNaksHandleErrorsAndContinues(t *testing.T) {
	tests := []struct {
		name       string
		nakErr     error
		wantNakLog bool
	}{
		{name: "nak succeeds"},
		{name: "nak fails", nakErr: errors.New("nak unavailable"), wantNakLog: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			msg := &stageMessageStub{data: []byte("not json"), nakErr: tt.nakErr}
			bus := &stageBusStub{fetches: []stageFetch{
				{message: msg},
				{err: errors.New("fetch after cancellation"), before: cancel},
			}}
			log, output := stageLoopLogger()
			delay, delays := noDelay(t)

			if code := runMainLoopWithDelay(ctx, bus, log, delay); code != 0 {
				t.Fatalf("runMainLoopWithDelay() = %d, want 0", code)
			}
			if msg.nakCalls != 1 || msg.ackCalls != 0 {
				t.Fatalf("(Nak calls, Ack calls) = (%d, %d), want (1, 0)", msg.nakCalls, msg.ackCalls)
			}
			if bus.fetchCalls != 2 {
				t.Fatalf("Fetch calls = %d, want 2", bus.fetchCalls)
			}
			if len(*delays) != 0 {
				t.Fatalf("delay calls = %v, want none", *delays)
			}
			if !strings.Contains(output.String(), "handle failed") {
				t.Fatalf("log output %q does not contain handle failure", output.String())
			}
			if got := strings.Contains(output.String(), "nak failed"); got != tt.wantNakLog {
				t.Fatalf("nak failure logged = %v, want %v; output %q", got, tt.wantNakLog, output.String())
			}
		})
	}
}

func TestRunMainLoopAcksSuccessfulHandleAndContinues(t *testing.T) {
	tests := []struct {
		name       string
		ackErr     error
		wantAckLog bool
	}{
		{name: "ack succeeds"},
		{name: "ack fails", ackErr: errors.New("ack unavailable"), wantAckLog: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			data := []byte(`{"providers":{"test":{"class":"openai"}}}`)
			msg := &stageMessageStub{data: data, ackErr: tt.ackErr}
			bus := &stageBusStub{
				fetches:       []stageFetch{{message: msg}},
				respondCancel: cancel,
			}
			log, output := stageLoopLogger()
			delay, delays := noDelay(t)

			if code := runMainLoopWithDelay(ctx, bus, log, delay); code != 0 {
				t.Fatalf("runMainLoopWithDelay() = %d, want 0", code)
			}
			if bus.respondCalls != 1 {
				t.Fatalf("Respond calls = %d, want 1", bus.respondCalls)
			}
			if msg.ackCalls != 1 || msg.nakCalls != 0 {
				t.Fatalf("(Ack calls, Nak calls) = (%d, %d), want (1, 0)", msg.ackCalls, msg.nakCalls)
			}
			if len(*delays) != 0 {
				t.Fatalf("delay calls = %v, want none", *delays)
			}
			if got := strings.Contains(output.String(), "ack failed"); got != tt.wantAckLog {
				t.Fatalf("ack failure logged = %v, want %v; output %q", got, tt.wantAckLog, output.String())
			}
			if !strings.Contains(output.String(), "archie-agent shutting down") {
				t.Fatalf("log output %q does not contain shutdown message", output.String())
			}
		})
	}
}

var (
	_ stageBus         = (*stageBusStub)(nil)
	_ eventbus.Message = (*stageMessageStub)(nil)
)
