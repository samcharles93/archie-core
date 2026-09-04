package nats

import "strings"

// keySeparator joins a service and an instance ID in a registry key. It is a
// dot rather than the research sketch's slash so that a per-service watch
// filter ("curator.*") matches "curator.inst-1" as two NATS subject tokens;
// a slash would make the whole key one opaque token no wildcard could match.
// See the package doc comment for the full reasoning.
const keySeparator = "."

// endpointKey is the heartbeat bucket key for one live instance.
func endpointKey(service, id string) string {
	return service + keySeparator + id
}

// endpointKeyPrefix is the watch/list filter matching every instance of a
// service. It is a single-token wildcard over the instance ID.
func endpointKeyPrefix(service string) string {
	return service + keySeparator + "*"
}

// idFromEndpointKey returns the instance ID carried by a registry key, or "" if
// the key does not belong to the given service (or carries an empty ID).
func idFromEndpointKey(service, key string) string {
	prefix := service + keySeparator
	if !strings.HasPrefix(key, prefix) {
		return ""
	}
	return strings.TrimPrefix(key, prefix)
}
