package access

import "context"

type actorContextKey struct{}

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
