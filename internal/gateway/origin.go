package gateway

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

type originKey struct{}

// withOrigin marks ctx as a turn of the conversation origin, so a task the
// turn spawns records where it came from.
func withOrigin(ctx context.Context, origin string) context.Context {
	return context.WithValue(ctx, originKey{}, origin)
}

// originFrom is the conversation ctx's turn belongs to, or empty.
func originFrom(ctx context.Context) string {
	origin, _ := ctx.Value(originKey{}).(string)
	return origin
}

func (r *Router) turnContext(ctx context.Context, in Inbound) context.Context {
	return withOrigin(ctx, messaging.Origin(r.sessionPlatform(in), in.Message.ConversationID))
}
