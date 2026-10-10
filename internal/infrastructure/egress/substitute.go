package egress

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// SentinelFor is the placeholder a container holds in place of service's
// key. The proxy swaps it for the real key only on requests to the
// service's own host, so a container never holds a key and cannot send one
// anywhere else.
func SentinelFor(service string) string { return Sentinel + ":" + service }

// Substitution swaps service's sentinel for its resolved key on requests to
// Host. It serves clients that put the key wherever their protocol wants it
// (a bearer header, an API-key header, a query parameter) without the proxy
// knowing each protocol.
type Substitution struct {
	Service string
	Host    string
}

type substitution struct {
	service string
	domain  pattern
}

func compileSubstitutions(subs []Substitution) []substitution {
	out := make([]substitution, 0, len(subs))
	for _, s := range subs {
		out = append(out, substitution{service: s.Service, domain: compilePattern(s.Host)})
	}
	return out
}

// substitute replaces each sentinel this host may receive in r's headers and
// query with its resolved key. A sentinel bound for another host is left as
// it is, so the upstream sees only a placeholder.
func (p *Proxy) substitute(ctx context.Context, s *Session, r *http.Request, host string, port int) error {
	for _, sub := range s.substitutions {
		if !sub.domain.matches(normalizeHost(host), port) {
			continue
		}
		sentinel := SentinelFor(sub.service)
		if !requestCarries(r, sentinel) {
			continue
		}
		if p.resolver == nil {
			return fmt.Errorf("credential %q has no resolver", sub.service)
		}
		secret, err := p.resolver.Resolve(ctx, s.token, sub.service)
		if errors.Is(err, ErrUnbound) {
			return fmt.Errorf("credential %q is not bound for this run", sub.service)
		}
		if err != nil {
			return fmt.Errorf("resolve credential %q: %w", sub.service, err)
		}
		for _, values := range r.Header {
			for i, v := range values {
				values[i] = strings.ReplaceAll(v, sentinel, secret)
			}
		}
		r.URL.RawQuery = strings.ReplaceAll(r.URL.RawQuery, sentinel, secret)
	}
	return nil
}

func requestCarries(r *http.Request, sentinel string) bool {
	if strings.Contains(r.URL.RawQuery, sentinel) {
		return true
	}
	for _, values := range r.Header {
		for _, v := range values {
			if strings.Contains(v, sentinel) {
				return true
			}
		}
	}
	return false
}
