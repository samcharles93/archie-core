// Package relay forwards a sandbox's proxy connections to archie's egress
// proxy. It runs in the one container that is on both a sandbox's isolated
// network and the host-reachable network, and it can reach exactly one
// address: it moves bytes to its fixed target and nothing else.
package relay

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

const dialTimeout = 10 * time.Second

// Serve accepts connections on ln and joins each to a new connection to
// target until ctx is cancelled.
func Serve(ctx context.Context, ln net.Listener, target string) error {
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		client, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		wg.Go(func() {
			join(ctx, client, target)
		})
	}
}

func join(ctx context.Context, client net.Conn, target string) {
	defer client.Close()
	upstream, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", target)
	if err != nil {
		return
	}
	defer upstream.Close()
	stop := context.AfterFunc(ctx, func() {
		_ = client.Close()
		_ = upstream.Close()
	})
	defer stop()
	done := make(chan struct{}, 2)
	go pipe(upstream, client, done)
	go pipe(client, upstream, done)
	<-done
	<-done
}

// pipe copies src to dst, then half-closes dst so the far side sees EOF
// while the other direction keeps flowing.
func pipe(dst, src net.Conn, done chan<- struct{}) {
	_, err := io.Copy(dst, src)
	if tcp, ok := dst.(*net.TCPConn); ok && (err == nil || errors.Is(err, net.ErrClosed)) {
		_ = tcp.CloseWrite()
	} else {
		_ = dst.Close()
	}
	done <- struct{}{}
}
