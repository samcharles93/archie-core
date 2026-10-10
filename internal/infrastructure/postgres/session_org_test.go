package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

// One org's chat sessions and messages are invisible to another, which can
// neither read nor overwrite them.
func TestChatSessionsStayInTheirOrg(t *testing.T) {
	acme := org.WithOrg(t.Context(), "acme")
	other := org.WithOrg(t.Context(), "other")
	store := gateway.NewPostgresSessionStore(pgstore.Pool(t))
	now := time.Now().UTC()
	if err := store.Save(acme, gateway.SessionContext{SessionID: "s1", Title: "acme plans", CreatedAt: now, LastActiveAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMessage(acme, "s1", messaging.Message{ID: "m1", Role: messaging.RoleUser, Text: "secret"}); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name string
		ctx  func() context.Context //nolint:containedctx // table input, not stored state
		sees bool
	}{
		{"the owning org", func() context.Context { return acme }, true},
		{"another org", func() context.Context { return other }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.ctx()
			listed, err := store.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			got, err := store.Get(ctx, "s1")
			if err != nil {
				t.Fatal(err)
			}
			messages, readErr := store.RecentMessages(ctx, "s1", 10)
			if sees := len(listed) == 1 && got != nil && readErr == nil && len(messages) == 1; sees != tt.sees {
				t.Fatalf("listed %d, got %v, messages %d (%v); want visible %v", len(listed), got != nil, len(messages), readErr, tt.sees)
			}
			if tt.sees {
				return
			}
			if err := store.Save(ctx, gateway.SessionContext{SessionID: "s1", Title: "taken"}); !errors.Is(err, gateway.ErrSessionNotFound) {
				t.Fatalf("overwrite err = %v, want ErrSessionNotFound", err)
			}
			if err := store.SaveMessage(ctx, "s1", messaging.Message{ID: "m2", Role: messaging.RoleUser, Text: "injected"}); !errors.Is(err, gateway.ErrSessionNotFound) {
				t.Fatalf("append err = %v, want ErrSessionNotFound", err)
			}
		})
	}
}
