package archied

import (
	"fmt"
	"strings"
)

// resolveServiceListen picks the address a service listener binds: the -listen
// flag when it was given, else the address the configuration names.
//
// Both services this repository runs used to take their address from a flag
// default alone, with no config key. The port a service binds and the port its
// clients dial were therefore two separate facts -- the flag default (or a
// hand-passed -listen) and [services.<name>].target -- with nothing keeping
// them in step. On a host whose default port was already taken, retargeting
// meant hand-writing an overlay and passing a matching flag, and
// the failure looked like a code fault: the listener died with "address already
// in use" while every client reported a gRPC handshake error against whatever
// else held the port.
//
// An empty result is an error, never a fallback: net.Listen treats "" as "any
// free port", so defaulting here would bind the service somewhere its clients
// do not look and report success while doing it.
func resolveServiceListen(service, flagValue, configured string) (string, error) {
	if v := strings.TrimSpace(flagValue); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(configured); v != "" {
		return v, nil
	}
	return "", fmt.Errorf(
		"%s: no listen address: pass -listen or set [services.%s].listen", service, service,
	)
}
