package egress

import (
	"context"
	"sync"
)

// GrantResolver is the production Resolver: it holds, per active run, only the
// secrets a caller explicitly Grant-ed it -- the intersection a run credential
// carries. It never derives a secret from anything else, so a service nobody
// granted, or granted to a different run, always answers ErrUnbound.
type GrantResolver struct {
	mu     sync.RWMutex
	grants map[string]map[string]string
}

// NewGrantResolver returns an empty resolver: no run may resolve anything
// until Grant is called for it.
func NewGrantResolver() *GrantResolver {
	return &GrantResolver{grants: map[string]map[string]string{}}
}

// Grant records exactly what one run may resolve, replacing whatever it held
// before -- a repeated Grant for the same run (a retry, or a narrower set)
// never merges with the last, since a service the caller dropped must not
// keep resolving from a stale entry.
func (g *GrantResolver) Grant(run string, secrets map[string]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.grants[run] = secrets
}

// RevokeGrant ends a run: nothing it was granted resolves after this call.
func (g *GrantResolver) RevokeGrant(run string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.grants, run)
}

// Resolve implements Resolver. A run with no Grant, or a service not among
// what it was granted, answers ErrUnbound -- the only error ambiguous enough
// for an optional credential to pass over (see Resolver's doc).
func (g *GrantResolver) Resolve(_ context.Context, run, service string) (string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	secrets, ok := g.grants[run]
	if !ok {
		return "", ErrUnbound
	}
	v, ok := secrets[service]
	if !ok {
		return "", ErrUnbound
	}
	return v, nil
}

var _ Resolver = (*GrantResolver)(nil)
