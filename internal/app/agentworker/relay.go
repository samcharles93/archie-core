package agentworker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/samcharles93/archie-core/internal/infrastructure/egress/relay"
)

// RunRelay forwards each listen address to its target until ctx is
// cancelled. Every listener binds before any serves, so a port conflict
// fails the relay at start; one forward failing later stops them all.
func RunRelay(ctx context.Context, forwards map[string]string) error {
	listeners := make(map[string]net.Listener, len(forwards))
	closeAll := func() {
		for _, ln := range listeners {
			_ = ln.Close()
		}
	}
	for listen := range forwards {
		ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listen)
		if err != nil {
			closeAll()
			return fmt.Errorf("relay listen %s: %w", listen, err)
		}
		listeners[listen] = ln
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for listen, ln := range listeners {
		target := forwards[listen]
		wg.Go(func() {
			if err := relay.Serve(ctx, ln, target); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("relay %s -> %s: %w", listen, target, err))
				mu.Unlock()
				cancel()
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}
