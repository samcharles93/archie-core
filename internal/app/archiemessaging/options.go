// Package archiemessaging composes the standalone Messaging Service: the external
// channel connections (Telegram, email, webhook) served against a Gateway running
// over its gRPC contract.
//
// docs/prds/messaging-service-boundary.md is the authority. The Messaging Service
// owns channel persistent connections and dispatches turns directly to Gateway's
// ChatContract over gRPC.
package archiemessaging

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Process defaults.
const (
	defaultGatewayTarget     = "127.0.0.1:8585"
	defaultDependencyTimeout = 5 * time.Second
	defaultShutdownTimeout   = 5 * time.Second
)

// ServiceTarget is one remote contract's endpoint and the service-to-service
// bearer token this process presents to it.
type ServiceTarget struct {
	Target string
	Token  string
}

// Options contains the process inputs for the Messaging Service.
type Options struct {
	Config  string
	Overlay string

	Gateway           ServiceTarget
	DependencyTimeout time.Duration
	ShutdownTimeout   time.Duration
}

func withDefaults(o Options) Options {
	if o.Gateway.Target == "" {
		o.Gateway.Target = defaultGatewayTarget
	}
	if o.DependencyTimeout <= 0 {
		o.DependencyTimeout = defaultDependencyTimeout
	}
	if o.ShutdownTimeout <= 0 {
		o.ShutdownTimeout = defaultShutdownTimeout
	}
	return o
}

func (o Options) validate() error {
	if o.Gateway.Target == "" {
		return errors.New("gateway target is required (pass -gateway-target or configure [services.gateway].target)")
	}
	if err := validateServiceTarget("gateway", o.Gateway); err != nil {
		return err
	}
	return nil
}

func validateServiceTarget(serviceName string, target ServiceTarget) error {
	host, _, err := net.SplitHostPort(target.Target)
	if err != nil {
		host = target.Target
	}
	if isLoopback(host) {
		return nil
	}
	if target.Token == "" {
		return fmt.Errorf("%s target %q is non-loopback and requires a bearer token", serviceName, target.Target)
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
