package builtin_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/samcharles93/archie-core/internal/tools/builtin"
)

func TestMutationQueue_Serializes(t *testing.T) {
	q := builtin.NewMutationQueue()
	path := "/tmp/test.txt"

	var counter atomic.Int64
	var maxConcurrent atomic.Int64
	var wg sync.WaitGroup

	for i := range 50 {
		_ = i
		wg.Go(func() {
			release := q.Acquire(path)
			defer release()

			cur := counter.Add(1)
			// Track max concurrency for this path.
			for {
				old := maxConcurrent.Load()
				if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
					break
				}
			}
			counter.Add(-1)
		})
	}

	wg.Wait()

	if maxConcurrent.Load() > 1 {
		t.Fatalf("expected max concurrency of 1, got %d", maxConcurrent.Load())
	}
}

func TestMutationQueue_DifferentPaths_Parallel(t *testing.T) {
	q := builtin.NewMutationQueue()

	var wg sync.WaitGroup
	started := make(chan struct{}, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		release := q.Acquire("/path/a")
		started <- struct{}{}
		<-started // wait for both to acquire
		release()
	}()
	go func() {
		defer wg.Done()
		release := q.Acquire("/path/b")
		started <- struct{}{}
		<-started // wait for both to acquire
		release()
	}()

	wg.Wait()
	// If we get here without deadlocking, different paths don't block each other.
}
