package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
)

// TestRegisterServiceIsTheSingleDeclaration pins criterion 3 of
// docs/prds/service-registry.md: a service is declared by one call carrying the
// five fields it actually has, and every later layer reads that declaration
// rather than restating the name.
func TestRegisterServiceIsTheSingleDeclaration(t *testing.T) {
	config.RegisterService(config.ServiceBoth, "widget", "127.0.0.1:7001", "127.0.0.1:7002", "WIDGET_TOKEN")

	spec, ok := config.LookupService("widget")
	if !ok {
		t.Fatal("LookupService(widget) = false, want the service just registered")
	}
	if spec.Context != config.ServiceBoth {
		t.Errorf("Context = %q, want %q", spec.Context, config.ServiceBoth)
	}
	if spec.Target != "127.0.0.1:7001" {
		t.Errorf("Target = %q, want the registered default", spec.Target)
	}
	if spec.Listen != "127.0.0.1:7002" {
		t.Errorf("Listen = %q, want the registered default", spec.Listen)
	}
	if spec.TokenEnv != "WIDGET_TOKEN" {
		t.Errorf("TokenEnv = %q, want the registered env var", spec.TokenEnv)
	}
}

// TestBuiltInServicesAreRegistered pins that the two services the daemon ships
// are declared through the same entry point as any other, with the asymmetry
// the old struct form left unstated: state has no default target, so the
// operator must supply one, while gateway does.
func TestBuiltInServicesAreRegistered(t *testing.T) {
	state, ok := config.LookupService("state")
	if !ok {
		t.Fatal("LookupService(state) = false, want the State Store registered")
	}
	if state.Target != "" {
		t.Errorf("state Target default = %q, want empty: services.state.target is operator-supplied", state.Target)
	}
	if state.Listen == "" {
		t.Error("state Listen default is empty, want an address: an empty listen reaches net.Listen as any free port")
	}
	if state.TokenEnv != "STATE_STORE_TOKEN" {
		t.Errorf("state TokenEnv = %q, want STATE_STORE_TOKEN", state.TokenEnv)
	}

	gateway, ok := config.LookupService("gateway")
	if !ok {
		t.Fatal("LookupService(gateway) = false, want the Gateway registered")
	}
	if gateway.Target == "" {
		t.Error("gateway Target default is empty, want an address: the gateway target is defaulted, not operator-supplied")
	}
	if gateway.TokenEnv != "GATEWAY_TOKEN" {
		t.Errorf("gateway TokenEnv = %q, want GATEWAY_TOKEN", gateway.TokenEnv)
	}
}

// TestUnregisteredServiceIsNotFound keeps the registry the authority on which
// names exist, which is what lets the loader report a typo'd [services.gatway]
// as an unknown key once Services is a map that would otherwise swallow it.
func TestUnregisteredServiceIsNotFound(t *testing.T) {
	if _, ok := config.LookupService("gatway"); ok {
		t.Error("LookupService(gatway) = true, want false for an unregistered name")
	}
}

// TestServicesGetReturnsTheNamedConnection pins lookup by name, which replaced
// per-service struct field access, so adding a service needs no new field.
func TestServicesGetReturnsTheNamedConnection(t *testing.T) {
	services := config.Services{
		"state": {Target: "10.0.0.1:9090", Listen: "0.0.0.0:9090", TargetToken: "explicit"},
	}
	got := services.Get("state")
	if got.Target != "10.0.0.1:9090" {
		t.Errorf("Get(state).Target = %q, want the configured target", got.Target)
	}
	if services.Get("absent") != (config.ServiceConnection{}) {
		t.Error("Get(absent) returned a non-zero connection, want the zero value")
	}
}

// TestResolvedTokenPrefersTheExplicitKey pins the precedence the two
// hand-copied resolvers used: the config key wins, and the registered env var
// is only a fallback.
func TestResolvedTokenPrefersTheExplicitKey(t *testing.T) {
	config.RegisterService(config.ServiceClient, "widget-explicit", "", "127.0.0.1:7003", "WIDGET_EXPLICIT_TOKEN")
	services := config.Services{"widget-explicit": {TargetToken: "from-config"}}

	getenv := func(string) string { return "from-env" }
	if got := services.ResolvedToken("widget-explicit", getenv); got != "from-config" {
		t.Errorf("ResolvedToken = %q, want the explicit target_token to win", got)
	}
}

// TestResolvedTokenFallsBackToTheRegisteredEnvVar is the half that the old
// stateStoreResolvedToken and gatewayResolvedToken duplicated, differing only
// in the env var name they hard-coded.
func TestResolvedTokenFallsBackToTheRegisteredEnvVar(t *testing.T) {
	config.RegisterService(config.ServiceClient, "widget-env", "", "127.0.0.1:7004", "WIDGET_ENV_TOKEN")
	services := config.Services{"widget-env": {}}

	var asked string
	getenv := func(name string) string {
		asked = name
		return "from-env"
	}
	if got := services.ResolvedToken("widget-env", getenv); got != "from-env" {
		t.Errorf("ResolvedToken = %q, want the env fallback", got)
	}
	if asked != "WIDGET_ENV_TOKEN" {
		t.Errorf("read env var %q, want the name from the registration", asked)
	}
}

// TestResolvedTokenOfUnregisteredServiceHasNoEnvFallback keeps an unknown name
// from reaching os.Getenv("") and returning something surprising.
func TestResolvedTokenOfUnregisteredServiceHasNoEnvFallback(t *testing.T) {
	services := config.Services{"gatway": {}}
	getenv := func(string) string {
		t.Error("getenv called for an unregistered service, want no env fallback")
		return "leaked"
	}
	if got := services.ResolvedToken("gatway", getenv); got != "" {
		t.Errorf("ResolvedToken = %q, want empty", got)
	}
}

// mustPanic runs fn and returns the recovered value, or fails if fn returned
// normally. Registration errors are programming errors in this repository's
// own init(), not operator input, so they abort the process rather than
// returning an error nobody is positioned to handle.
func mustPanic(t *testing.T, fn func()) (recovered any) {
	t.Helper()
	defer func() { recovered = recover() }()
	fn()
	t.Fatal("RegisterService returned normally, want a panic")
	return nil
}

// TestRegisterServiceRejectsAnUnknownContext makes Context load-bearing. It
// documented which addresses a service must end up with, but nothing read it,
// so a typo'd context was accepted and silently meant nothing.
func TestRegisterServiceRejectsAnUnknownContext(t *testing.T) {
	got := mustPanic(t, func() {
		config.RegisterService("clientt", "widget-badcontext", "", "127.0.0.1:7005", "T")
	})
	if !strings.Contains(fmt.Sprint(got), "clientt") {
		t.Errorf("panic = %v, want it to name the invalid context", got)
	}
}

// TestRegisterServiceRejectsAHostedServiceWithNoListen pins the requirement
// Context exists to express: a service this repository hosts must have a
// default bind address, because an empty one reaches net.Listen as "any free
// port".
func TestRegisterServiceRejectsAHostedServiceWithNoListen(t *testing.T) {
	for _, hosted := range []string{config.ServiceServer, config.ServiceBoth} {
		t.Run(hosted, func(t *testing.T) {
			got := mustPanic(t, func() {
				config.RegisterService(hosted, "widget-nolisten-"+hosted, "127.0.0.1:1", "", "T")
			})
			if !strings.Contains(fmt.Sprint(got), "listen") {
				t.Errorf("panic = %v, want it to name the missing listen address", got)
			}
		})
	}
}

// TestRegisterServiceAcceptsAClientWithNoTargetDefault keeps the State Store's
// own shape legal: a dialled-only service may register an empty target, which
// is how "the operator must supply this" is stated.
func TestRegisterServiceAcceptsAClientWithNoTargetDefault(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RegisterService panicked on a legal client registration: %v", r)
		}
	}()
	config.RegisterService(config.ServiceClient, "widget-clientok", "", "127.0.0.1:7006", "T")
}

// TestRequireTargetReportsAnAbsentOperatorSuppliedTarget moves the "target is
// required" check off two hand-written consumer copies and onto the registry,
// which is what declared the target operator-supplied in the first place.
func TestRequireTargetReportsAnAbsentOperatorSuppliedTarget(t *testing.T) {
	for _, tt := range []struct {
		name    string
		target  string
		want    string
		wantErr bool
	}{
		{name: "absent", wantErr: true},
		{name: "blank", target: " \t\n", wantErr: true},
		{name: "set", target: "10.0.0.1:9090", want: "10.0.0.1:9090"},
		{name: "set with padding", target: "  10.0.0.1:9090  ", want: "10.0.0.1:9090"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			services := config.Services{config.ServiceNameState: {Target: tt.target}}
			got, err := services.RequireTarget(config.ServiceNameState)
			if tt.wantErr {
				if err == nil {
					t.Fatal("RequireTarget returned no error for an empty target")
				}
				if !strings.Contains(err.Error(), "services.state.target") {
					t.Errorf("error = %v, want it to name the config key", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("RequireTarget: %v", err)
			}
			if got != tt.want {
				t.Errorf("RequireTarget = %q, want %q", got, tt.want)
			}
		})
	}
}
