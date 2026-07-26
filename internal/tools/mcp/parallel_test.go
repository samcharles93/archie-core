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

func TestCallToolSerializesByDefault(t *testing.T) {
	b := &blockingSender{release: make(chan struct{})}
	c := NewClient(b, "test-server")

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

func TestCallToolParallelWhenOptedIn(t *testing.T) {
	b := &blockingSender{release: make(chan struct{})}
	c := NewClient(b, "test-server", WithParallelToolCalls(true))

	const n = 5
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			_, _ = c.CallTool(context.Background(), "slow", nil)
		})
	}

	// All n calls should be able to reach Send concurrently since nothing
	// serializes them.
	deadline := time.Now().Add(2 * time.Second)
	for b.MaxInFlight() < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := b.MaxInFlight(); got != n {
		t.Errorf("MaxInFlight = %d, want %d (parallel opt-in)", got, n)
	}

	close(b.release)
	wg.Wait()
}

func TestCallToolSerializedStillCompletesAllCalls(t *testing.T) {
	b := &blockingSender{release: make(chan struct{})}
	close(b.release) // never actually blocks  --  just verifying correctness, not timing
	c := NewClient(b, "test-server")

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
	c1 := NewClient(b1, "server-1")
	c2 := NewClient(b2, "server-2")

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
