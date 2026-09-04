package nats

import (
	"encoding/json"
	"fmt"

	"github.com/samcharles93/archie-core/internal/servicediscovery"
)

// endpointJSON is the on-disk (KV value) encoding of a registered endpoint. It
// is deliberately the contract type so a consumer reading raw records gets the
// same shape ServiceRegistry hands back.
func encodeEndpoint(ep servicediscovery.Endpoint) ([]byte, error) {
	b, err := json.Marshal(ep)
	if err != nil {
		return nil, fmt.Errorf("encode endpoint %s/%s: %w", ep.Service, ep.ID, err)
	}
	return b, nil
}

// decodeEndpoint parses a KV value back into an Endpoint. It reports false for
// a value it cannot parse.
func decodeEndpoint(value []byte) (servicediscovery.Endpoint, bool) {
	var ep servicediscovery.Endpoint
	if err := json.Unmarshal(value, &ep); err != nil {
		return servicediscovery.Endpoint{}, false
	}
	return ep, true
}
