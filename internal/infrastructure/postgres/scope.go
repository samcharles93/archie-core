package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/samcharles93/archie-core/internal/domain/org"
)

// scopeOrg is the org a request's reads are confined to, or NULL for an
// internal service acting across orgs.
func scopeOrg(ctx context.Context) pgtype.Text {
	id, ok := org.Scope(ctx)
	return pgtype.Text{String: string(id), Valid: ok}
}
