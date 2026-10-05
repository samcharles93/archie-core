package egress

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"sync"
)

// Tunnel is an allow-list forward proxy that never terminates TLS: CONNECT is
// spliced byte for byte to an admitted host, and absolute-form http requests
// are forwarded. Extensions get it rather than Proxy because they carry their
// own credentials and never trust the daemon CA.
type Tunnel struct {
	rules rules
	dial  func(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewTunnel admits the hosts allow names, in network-policy@1 form. A host
// named exactly may resolve to a private address, since the operator named
// it (a LAN forge); a wildcard match may not. Loopback is refused whatever
// is named: it would reach archie's own services.
func NewTunnel(allow []string) *Tunnel {
	t := &Tunnel{rules: rules{allow: compilePatterns(allow)}}
	t.dial = t.dialGuarded
	return t
}

func (t *Tunnel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defaultPort, hostport := 443, r.Host
	if r.Method != http.MethodConnect {
		if !r.URL.IsAbs() || r.URL.Scheme != "http" {
			http.Error(w, "only CONNECT and absolute-form http requests are proxied", http.StatusBadRequest)
			return
		}
		defaultPort, hostport = 80, r.URL.Host
	}
	host, port, err := splitTarget(hostport, defaultPort)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target := net.JoinHostPort(host, strconv.Itoa(port))
	if !t.rules.allows(host, port) {
		http.Error(w, "egress to "+target+" is not allowed", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodConnect {
		t.splice(w, r, target)
		return
	}
	rp := &httputil.ReverseProxy{
		Transport:     &http.Transport{DialContext: t.dial},
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Host = target
			pr.Out.Header.Del("Proxy-Connection")
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "upstream "+target+": "+err.Error(), http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

func (t *Tunnel) splice(w http.ResponseWriter, r *http.Request, target string) {
	upstream, err := t.dial(r.Context(), "tcp", target)
	if err != nil {
		http.Error(w, "upstream "+target+": "+err.Error(), http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "connection cannot be tunnelled", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	defer func() { _ = client.Close(); _ = upstream.Close() }()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = io.Copy(upstream, buffered)
		closeWrite(upstream)
	})
	_, _ = io.Copy(client, upstream)
	closeWrite(client)
	wg.Wait()
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}

// dialGuarded applies the Proxy's upstream guard, counting a host the allow
// list names exactly as naming every address it resolves to.
func (t *Tunnel) dialGuarded(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	named := t.namesHost(normalizeHost(host))
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	guardErr := fmt.Errorf("%s resolved to no address", host)
	for _, a := range addrs {
		if guardErr = guardUpstream(a.IP, named); guardErr != nil {
			continue
		}
		return (&net.Dialer{Timeout: handshakeTimeout}).DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
	}
	return nil, guardErr
}

func (t *Tunnel) namesHost(host string) bool {
	for _, p := range t.rules.allow {
		if !p.everything && p.suffix == "" && p.host == host {
			return true
		}
	}
	return false
}
