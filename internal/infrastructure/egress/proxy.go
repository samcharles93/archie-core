package egress

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/docker/sandbox-kit-spec/v3/spec"
)

// proxyUser is the fixed user name in a sandbox's proxy URL; the session
// token is the password.
const proxyUser = "archie"

const handshakeTimeout = 10 * time.Second

// cgnat is shared address space (RFC 6598), which overlay networks use for
// host addresses; it is as internal as RFC 1918 space.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// Session is one sandbox container's standing with the proxy: its token and
// its Kit's network policy. It starts in the install phase; the runner moves
// it to runtime before the workload's entrypoint starts.
type Session struct {
	token      string
	run        string
	install    rules
	runtime    rules
	injections []injection
	atRun      atomic.Bool
}

// Token is the secret the container presents as its proxy password.
func (s *Session) Token() string { return s.token }

// EnterRuntime switches the session from install-phase to runtime-phase
// egress. It is one-way: install egress never reopens.
func (s *Session) EnterRuntime() { s.atRun.Store(true) }

func (s *Session) rules() rules {
	if s.atRun.Load() {
		return s.runtime
	}
	return s.install
}

// ProxyOptions are the proxy's upstream dependencies. Both are optional:
// the zero value dials the network through the upstream guard and verifies
// upstream TLS against the system roots.
type ProxyOptions struct {
	UpstreamRoots *x509.CertPool
	Dial          func(ctx context.Context, network, addr string) (net.Conn, error)
	// Resolver supplies credentials for injection. Without one, a request
	// that needs a required credential is refused.
	Resolver Resolver
}

// SessionOptions describe one container's run: the run credential it acts
// under, and its Kit's network policy and credential requests.
type SessionOptions struct {
	Run         string
	Network     *spec.PhasedNetwork
	Credentials []spec.CredentialCapability
}

// Proxy is the egress proxy sandbox containers reach through their relay.
// It authenticates each request to a session, applies that session's
// network policy, terminates TLS with the daemon CA, and forwards to the
// real upstream with verified TLS.
type Proxy struct {
	ca        *CA
	dial      func(ctx context.Context, network, addr string) (net.Conn, error)
	transport *http.Transport
	resolver  Resolver

	mu       sync.RWMutex
	sessions map[string]*Session
}

type sessionKey struct{}

func NewProxy(ca *CA, opts ProxyOptions) *Proxy {
	p := &Proxy{ca: ca, dial: opts.Dial, resolver: opts.Resolver, sessions: map[string]*Session{}}
	p.transport = &http.Transport{
		DialContext:         p.dialUpstream,
		TLSClientConfig:     &tls.Config{RootCAs: opts.UpstreamRoots, MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: handshakeTimeout,
	}
	return p
}

// Register opens a session for one container. A nil network policy allows
// no egress in either phase.
func (p *Proxy) Register(opts SessionOptions) (*Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	s := &Session{token: hex.EncodeToString(raw), run: opts.Run, injections: compileInjections(opts.Credentials)}
	if opts.Network != nil {
		s.install, s.runtime = compileRules(opts.Network.Install), compileRules(opts.Network.Runtime)
	}
	p.mu.Lock()
	p.sessions[s.token] = s
	p.mu.Unlock()
	return s, nil
}

// Revoke ends a session. Requests already forwarded finish; new ones fail.
func (p *Proxy) Revoke(token string) {
	p.mu.Lock()
	delete(p.sessions, token)
	p.mu.Unlock()
}

func (p *Proxy) session(r *http.Request) *Session {
	encoded, ok := strings.CutPrefix(r.Header.Get("Proxy-Authorization"), "Basic ")
	if !ok {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil
	}
	user, token, ok := strings.Cut(string(decoded), ":")
	if !ok || user != proxyUser {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sessions[token]
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s := p.session(r)
	if s == nil {
		w.Header().Set("Proxy-Authenticate", `Basic realm="archie"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	switch {
	case r.Method == http.MethodConnect:
		p.serveConnect(w, r, s)
	case r.URL.IsAbs() && r.URL.Scheme == "http":
		host, port, err := splitTarget(r.URL.Host, 80)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !s.rules().allows(host, port) {
			http.Error(w, "egress to "+net.JoinHostPort(host, strconv.Itoa(port))+" is not allowed", http.StatusForbidden)
			return
		}
		p.forward(r.Context(), w, r, s, "http", host, port)
	default:
		http.Error(w, "only CONNECT and absolute-form http requests are proxied", http.StatusBadRequest)
	}
}

func (p *Proxy) serveConnect(w http.ResponseWriter, r *http.Request, s *Session) {
	host, port, err := splitTarget(r.Host, 443)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.rules().allows(host, port) {
		http.Error(w, "egress to "+net.JoinHostPort(host, strconv.Itoa(port))+" is not allowed", http.StatusForbidden)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "connection cannot be tunnelled", http.StatusInternalServerError)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		_ = conn.Close()
		return
	}
	tlsConn := tls.Server(conn, &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			// The tunnel was admitted for host; a handshake naming another
			// server would let the client reach it under host's permission.
			if hello.ServerName != "" && normalizeHost(hello.ServerName) != host {
				return nil, fmt.Errorf("TLS server name %q does not match tunnel host %q", hello.ServerName, host)
			}
			return p.ca.leaf(host)
		},
	})
	handshakeCtx, cancel := context.WithTimeout(r.Context(), handshakeTimeout)
	err = tlsConn.HandshakeContext(handshakeCtx)
	cancel()
	if err != nil {
		_ = tlsConn.Close()
		return
	}
	tunnel := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqHost, _, err := splitTarget(r.Host, port)
			if err != nil || reqHost != host {
				http.Error(w, "request host does not match its tunnel", http.StatusMisdirectedRequest)
				return
			}
			if !s.rules().allows(host, port) {
				http.Error(w, "egress to "+net.JoinHostPort(host, strconv.Itoa(port))+" is no longer allowed", http.StatusForbidden)
				return
			}
			p.forward(r.Context(), w, r, s, "https", host, port)
		}),
		ReadHeaderTimeout: handshakeTimeout,
	}
	_ = tunnel.Serve(newOneConnListener(tlsConn))
}

func (p *Proxy) forward(ctx context.Context, w http.ResponseWriter, r *http.Request, s *Session, scheme, host string, port int) {
	target := net.JoinHostPort(host, strconv.Itoa(port))
	// Injection happens before anything is sent, so a credential that
	// cannot be resolved never reaches upstream half-applied.
	if err := p.inject(ctx, s, r, host, port); err != nil {
		http.Error(w, "egress to "+target+": "+err.Error(), http.StatusBadGateway)
		return
	}
	rp := &httputil.ReverseProxy{
		Transport:     p.transport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = scheme
			pr.Out.URL.Host = target
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Del("Proxy-Authorization")
			pr.Out.Header.Del("Proxy-Connection")
			pr.Out = pr.Out.WithContext(context.WithValue(ctx, sessionKey{}, s))
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "upstream "+target+": "+err.Error(), http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

// dialUpstream resolves and dials an upstream, refusing addresses that
// would turn the proxy into a path back into the host or its networks.
func (p *Proxy) dialUpstream(ctx context.Context, network, addr string) (net.Conn, error) {
	if p.dial != nil {
		return p.dial(ctx, network, addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	s, _ := ctx.Value(sessionKey{}).(*Session)
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
	}
	var guardErr error
	for _, ip := range ips {
		named := s != nil && s.rules().namesIP(ip)
		if guardErr = guardUpstream(ip, named); guardErr != nil {
			continue
		}
		return (&net.Dialer{Timeout: handshakeTimeout}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
	if guardErr == nil {
		guardErr = fmt.Errorf("%s resolved to no address", host)
	}
	return nil, guardErr
}

// guardUpstream refuses loopback and unspecified addresses always, and
// private, link-local, shared and multicast addresses unless the policy
// named that exact IP.
func guardUpstream(ip net.IP, namedByPolicy bool) error {
	if ip.IsLoopback() || ip.IsUnspecified() {
		return fmt.Errorf("upstream address %s is the host itself", ip)
	}
	internal := ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || cgnat.Contains(ip)
	if internal && !namedByPolicy {
		return fmt.Errorf("upstream address %s is internal and not named by the network policy", ip)
	}
	return nil
}

func splitTarget(hostport string, defaultPort int) (string, int, error) {
	if !hasPort(hostport) {
		return normalizeHost(strings.Trim(hostport, "[]")), defaultPort, nil
	}
	host, portText, err := net.SplitHostPort(hostport)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port in %q", hostport)
	}
	return normalizeHost(host), port, nil
}

// hasPort reports whether hostport ends in a port: a bracketed IPv6 literal
// followed by one, or a host with exactly one colon.
func hasPort(hostport string) bool {
	if strings.HasPrefix(hostport, "[") {
		return strings.Contains(hostport, "]:")
	}
	return strings.Count(hostport, ":") == 1
}

// oneConnListener serves exactly one already-accepted connection, so an
// http.Server can drive a hijacked tunnel.
type oneConnListener struct {
	conn net.Conn
	once sync.Once
	done chan struct{}
}

func newOneConnListener(conn net.Conn) *oneConnListener {
	return &oneConnListener{conn: conn, done: make(chan struct{})}
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() { c = &closeNotifyConn{Conn: l.conn, done: l.done} })
	if c != nil {
		return c, nil
	}
	<-l.done
	return nil, errors.New("tunnel closed")
}

func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

type closeNotifyConn struct {
	net.Conn
	once sync.Once
	done chan struct{}
}

func (c *closeNotifyConn) Close() error {
	c.once.Do(func() { close(c.done) })
	return c.Conn.Close()
}
