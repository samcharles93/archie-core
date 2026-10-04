package access

import "context"

type (
	actorContextKey     struct{}
	sourceContextKey    struct{}
	principalContextKey struct{}
)

// WithActor attaches who a State Store call is made by, so an audit row
// records the caller the server authenticated rather than a constant.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorContextKey{}, actor)
}

// ActorFromContext returns the caller WithActor attached, or "unknown".
func ActorFromContext(ctx context.Context) string {
	if actor, ok := ctx.Value(actorContextKey{}).(string); ok && actor != "" {
		return actor
	}
	return "unknown"
}

// WithSource attaches the service a call came through.
func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sourceContextKey{}, source)
}

// SourceFromContext returns the service WithSource attached, or "unknown".
func SourceFromContext(ctx context.Context) string {
	if source, ok := ctx.Value(sourceContextKey{}).(string); ok && source != "" {
		return source
	}
	return "unknown"
}

// WithPrincipal attaches the principal a request acts as.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, p)
}

// PrincipalFromContext returns the principal WithPrincipal attached.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalContextKey{}).(Principal)
	return p, ok
}
