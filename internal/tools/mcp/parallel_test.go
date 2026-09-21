package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingSender is a Transport double whose Send blocks until release is
// closed, so tests can observe whether multiple calls are in flight at
// once (parallel) or one completes before the next starts (serialized).
type blockingSender struct {
	release chan struct{}
	// entered receives one token per Send that starts, before that Send
	// blocks on release. A test counts the tokens to learn how many calls
	// the client allowed to overlap; unlike a sampled maximum, a token is
	// only ever sent by a call that really reached Send.
	entered chan struct{}

	mu          sync.Mutex
	inFlight    int
	maxInFlight int
}

func (b *blockingSender) Send(_ context.Context, body []byte) ([]byte, error) {
	b.mu.Lock()
	b.inFlight++
	if b.inFlight > b.maxInFlight {
		b.maxInFlight = b.inFlight
	}
	b.mu.Unlock()

	if b.entered != nil {
		b.entered <- struct{}{}
	}
	<-b.release

	b.mu.Lock()
	b.inFlight--
	b.mu.Unlock()

	var msg Message
	_ = json.Unmarshal(body, &msg)
	return []byte(`{"jsonrpc":"2.0","id":` + string(msg.ID) + `,"result":{"content":[{"type":"text","text":"ok"}]}}`), nil
}

func (b *blockingSender) Notify(context.Context, []byte) error { return nil }

func (b *blockingSender) MaxInFlight() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.maxInFlight
}

// overlapWindow bounds how long a test gives calls to reach Send. A call
// that is genuinely allowed to overlap reaches Send microseconds after its
// goroutine starts, so a call still absent after this window is being held
// back by the client, not by the scheduler.
const overlapWindow = 500 * time.Millisecond

// enteredCount reports how many of n calls reached Send within window.
func enteredCount(entered <-chan struct{}, n int, window time.Duration) int {
	count := 0
	for range n {
		select {
		case <-entered:
			count++
		case <-time.After(window):
			return count
		}
	}
	return count
}

// TestCallToolParallelToolCallsControlsSameServerOverlap pins the per-server
// flag: a server that has not opted in keeps one tools/call in flight, and a
// server that opted in lets its calls overlap.
func TestCallToolParallelToolCallsControlsSameServerOverlap(t *testing.T) {
	tests := []struct {
		name              string
		parallelToolCalls bool
		wantInFlight      int
	}{
		{
			name:         "server that has not opted in serializes tools/call",
			wantInFlight: 1,
		},
		{
			name:              "opted-in server overlaps tools/call",
			parallelToolCalls: true,
			wantInFlight:      2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &blockingSender{entered: make(chan struct{}, 2), release: make(chan struct{})}
			c := NewClient(b, "test-server", tt.parallelToolCalls)

			errs := make([]error, 2)
			started := make(chan struct{}, len(errs))
			var wg sync.WaitGroup
			for i := range errs {
				wg.Go(func() {
					started <- struct{}{}
					_, errs[i] = c.CallTool(context.Background(), "slow", nil)
				})
			}
			// Both callers are inside CallTool before either is measured, so
			// the count is what the client allowed, not the scheduler's.
			for range errs {
				<-started
			}

			if got := enteredCount(b.entered, len(errs), overlapWindow); got != tt.wantInFlight {
				t.Fatalf("calls in flight at once = %d, want %d", got, tt.wantInFlight)
			}

			close(b.release)
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Fatalf("call %d: %v", i, err)
				}
			}
		})
	}
}

// TestParallelToolCallsIsPerServer pins the isolation half of the flag: one
// server opting in must not un-serialize another server in the same process,
// even while the opted-in server's own calls are overlapping.
func TestParallelToolCallsIsPerServer(t *testing.T) {
	optedIn := &blockingSender{entered: make(chan struct{}, 2), release: make(chan struct{})}
	serialized := &blockingSender{entered: make(chan struct{}, 2), release: make(chan struct{})}
	parallel := NewClient(optedIn, "opted-in", true)
	sequential := NewClient(serialized, "default", false)

	call := func(c *Client) {
		if _, err := c.CallTool(context.Background(), "slow", nil); err != nil {
			t.Errorf("CallTool: %v", err)
		}
	}

	var parallelWG sync.WaitGroup
	for range 2 {
		parallelWG.Go(func() { call(parallel) })
	}
	if got := enteredCount(optedIn.entered, 2, overlapWindow); got != 2 {
		t.Fatalf("opted-in server calls in flight = %d, want 2", got)
	}

	// The opted-in server's calls are still open when the other server is
	// measured, so a leak of its parallelism would show up here.
	var sequentialWG sync.WaitGroup
	for range 2 {
		sequentialWG.Go(func() { call(sequential) })
	}
	if got := enteredCount(serialized.entered, 2, overlapWindow); got != 1 {
		t.Fatalf("server that did not opt in calls in flight = %d, want 1: opting one server in must not free another server's calls", got)
	}

	close(optedIn.release)
	close(serialized.release)
	parallelWG.Wait()
	sequentialWG.Wait()
}

func TestCallToolSerializesByDefault(t *testing.T) {
	b := &blockingSender{release: make(chan struct{})}
	c := NewClient(b, "test-server", false)

	const n = 5
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			_, _ = c.CallTool(context.Background(), "slow", nil)
		})
	}

	// Give goroutines a moment to reach Send; with serialization, only one
	// should ever be blocked in Send at a time.
	time.Sleep(50 * time.Millisecond)
	if got := b.MaxInFlight(); got > 1 {
		t.Errorf("MaxInFlight = %d, want 1 (default is serialized)", got)
	}

	close(b.release)
	wg.Wait()
}

func TestCallToolSerializedStillCompletesAllCalls(t *testing.T) {
	b := &blockingSender{release: make(chan struct{})}
	close(b.release) // never actually blocks  --  just verifying correctness, not timing
	c := NewClient(b, "test-server", false)

	const n = 10
	var completed atomic.Int32
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			if _, err := c.CallTool(context.Background(), "fast", nil); err == nil {
				completed.Add(1)
			}
		})
	}
	wg.Wait()

	if got := completed.Load(); got != n {
		t.Errorf("completed = %d, want %d", got, n)
	}
}

func TestCallToolSerializationDoesNotBlockDifferentClients(t *testing.T) {
	// Serialization is per-Client (per-server), not global  --  two
	// independent Clients (different servers) must not block each other.
	b1 := &blockingSender{release: make(chan struct{})}
	b2 := &blockingSender{release: make(chan struct{})}
	close(b2.release) // c2's own call should complete immediately, unblocked
	c1 := NewClient(b1, "server-1", false)
	c2 := NewClient(b2, "server-2", false)

	done := make(chan struct{})
	go func() {
		_, _ = c1.CallTool(context.Background(), "slow", nil)
		close(done)
	}()

	// c1's call is now blocked in Send. c2 must still be able to proceed
	// immediately  --  it has its own serialization lock.
	select {
	case <-done:
		t.Fatal("c1's call should still be blocked")
	case <-time.After(20 * time.Millisecond):
	}

	c2Done := make(chan struct{})
	go func() {
		_, _ = c2.CallTool(context.Background(), "fast", nil)
		close(c2Done)
	}()
	select {
	case <-c2Done:
	case <-time.After(time.Second):
		t.Fatal("c2's call should not be blocked by c1's in-flight call")
	}

	close(b1.release)
	<-done
}
