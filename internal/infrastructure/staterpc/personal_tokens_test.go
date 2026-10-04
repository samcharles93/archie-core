package staterpc

import (
	"context"
	"strings"
	"testing"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
)

type tokenCalls struct {
	identity.PersonalTokenStore
	owner identity.IdentityID
}

func (t *tokenCalls) BindSubject(_ context.Context, id identity.IdentityID, _ identity.Subject, _ identity.Audit) error {
	t.owner = id
	return nil
}

// TestPersonalTokenOwner pins that a token binds to the caller's own
// principal and that a call with no signed-in person is refused.
func TestPersonalTokenOwner(t *testing.T) {
	id := strings.Repeat("ab", 32)
	tests := []struct {
		name      string
		principal *access.Principal
		wantOwner identity.IdentityID
	}{
		{"signed-in person owns the token", &access.Principal{IdentityID: "alice"}, "alice"},
		{"no principal is refused", nil, ""},
		{"shared token owner is refused", &access.Principal{IdentityID: identity.SystemID}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &tokenCalls{}
			s := &server{deps: Deps{PersonalTokens: store}}
			ctx := context.Background()
			if tt.principal != nil {
				ctx = access.WithPrincipal(ctx, *tt.principal)
			}
			_, err := s.AddPersonalToken(ctx, &pb.AddPersonalTokenRequest{Id: id})
			if (err == nil) != (tt.wantOwner != "") || store.owner != tt.wantOwner {
				t.Fatalf("err %v, bound to %q, want %q", err, store.owner, tt.wantOwner)
			}
		})
	}
}
