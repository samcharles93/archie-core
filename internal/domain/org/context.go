package org

import "context"

// orgContextKey is the unexported key the acting org travels under. A named
// struct type, not a string, so no other package can collide with it.
type orgContextKey struct{}

// WithOrg attaches the org a request acts in. Every control-plane request
// reads it back with OrgFromContext, so the org reaches the wire from one
// place: the context the caller's principal put it on.
func WithOrg(ctx context.Context, id OrgID) context.Context {
	return context.WithValue(ctx, orgContextKey{}, id)
}

// OrgFromContext returns the org a request acts in, or DefaultOrgID when the
// context carries none. DefaultOrgID is the single-operator install's org and
// the org every record written before orgs existed belongs to.
func OrgFromContext(ctx context.Context) OrgID {
	if id, ok := ctx.Value(orgContextKey{}).(OrgID); ok && id != "" {
		return id
	}
	return DefaultOrgID
}

type scopeContextKey struct{}

// WithScope confines a request's reads to one org. A request without a scope
// is an internal service acting across orgs, such as the daemon running every
// org's work or webhook intake resolving a source by its path.
func WithScope(ctx context.Context, id OrgID) context.Context {
	return context.WithValue(ctx, scopeContextKey{}, id)
}

// Scope returns the org WithScope confined ctx to.
func Scope(ctx context.Context) (OrgID, bool) {
	id, ok := ctx.Value(scopeContextKey{}).(OrgID)
	return id, ok && id != ""
}
