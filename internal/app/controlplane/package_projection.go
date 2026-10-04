package controlplane

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// projectDocument edits one org resource on behalf of an installed package.
// Each attempt re-reads the stored document and re-applies edit, so an
// operator edit made during the projection is folded in, and only a
// persistently moving document refuses it. edit receives nil for a resource
// the org never wrote.
func projectDocument(ctx context.Context, store ResourceStore, orgID, kind, source, requestPrefix string, edit func(current []byte) ([]byte, error)) error {
	requestID := requestPrefix + ":" + strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
	for attempt := 0; ; attempt++ {
		var current []byte
		var version int64
		resource, err := store.Resource(ctx, orgID, kind)
		switch {
		case errors.Is(err, storecontract.ErrResourceNotFound):
		case err != nil:
			return err
		default:
			current, version = resource.Value, resource.Version
		}
		value, err := edit(current)
		if err != nil {
			return err
		}
		_, err = store.PutResource(ctx, storecontract.ResourceWrite{
			OrgID: orgID, Kind: kind, Value: value, ExpectedVersion: version,
			Actor: projectionActor, Source: source, RequestID: requestID,
		})
		if errors.Is(err, storecontract.ErrResourceVersionConflict) && attempt < projectWriteAttempts-1 {
			continue
		}
		if err != nil {
			return fmt.Errorf("write %s: %w", kind, err)
		}
		return nil
	}
}
