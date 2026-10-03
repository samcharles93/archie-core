// Package servicediscovery is the broker-neutral service registry contract:
// resolve a service's live endpoints and watch membership changes. A service
// never installed returns ErrNotInstalled; an installed service with no live
// endpoints returns no endpoints and no error.
package servicediscovery
