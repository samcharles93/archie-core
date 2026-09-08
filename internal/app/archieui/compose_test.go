package archieui

import (
	"log/slog"
	"reflect"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/store"
)

// isNil reports whether v is nil, including a typed nil func or pointer
// carried inside a non-nil interface -- a plain `v != nil` reports those as
// present.
func isNil(v any) bool {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return true
	}
	switch rv.Kind() {
	case reflect.Func, reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}

// TestComposeUIServerHoldsNoDaemonState is the acceptance criterion the parent
// bead names first: the UI process's dashboard "no longer receives the
// daemon's live config.Holder". It pins the whole composition: the two
// contract-backed fields hold remote clients, and every daemon-local runtime
// handle stays nil so the process cannot hold in-process daemon state by
// accident (docs/prds/ui-service-boundary.md, "Boundary and ownership").
func TestComposeUIServerHoldsNoDaemonState(t *testing.T) {
	stateConn, err := grpc.NewClient("passthrough:///127.0.0.1:1", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("state client: %v", err)
	}
	defer stateConn.Close()
	gatewayConn, err := grpc.NewClient("passthrough:///127.0.0.1:1", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("gateway client: %v", err)
	}
	defer gatewayConn.Close()

	srv := compose(deps{
		Options: Options{Listen: "127.0.0.1:8484", Token: "tok"},
		Log:     slog.New(slog.DiscardHandler),
		Store:   staterpc.NewClient(stateConn),
		Chat:    gatewayrpc.NewClient(gatewayConn),
	})

	if srv.Cfg != nil {
		t.Fatal("UI server holds a config.Holder; the UI process must not share the daemon's live configuration (docs/prds/ui-service-boundary.md:95-96)")
	}
	if _, ok := srv.Store.(*staterpc.Client); !ok {
		t.Fatalf("Store = %T, want *staterpc.Client", srv.Store)
	}
	if _, ok := srv.Store.(*store.Store); ok {
		t.Fatal("Store is the concrete *store.Store; the UI process must not open archie.db")
	}
	if srv.Chat == nil {
		t.Fatal("Chat is nil; the UI process must dial the Gateway contract")
	}
	if _, ok := srv.Chat.Contract.(*gatewayrpc.Client); !ok {
		t.Fatalf("Chat.Contract = %T, want *gatewayrpc.Client", srv.Chat.Contract)
	}
	if srv.Chat.Updates != nil {
		t.Error("Chat.Updates is wired; host-local install/restart stays with the daemon")
	}
	if srv.Token != "tok" {
		t.Fatalf("Token = %q, want the UI process's own token", srv.Token)
	}

	unwired := map[string]any{
		"UpdateConfig":      srv.UpdateConfig,
		"ResetConfig":       srv.ResetConfig,
		"ConfigOverrides":   srv.ConfigOverrides,
		"UpdateRepoField":   srv.UpdateRepoField,
		"LastReload":        srv.LastReload,
		"ReloadChannel":     srv.ReloadChannel,
		"RunningVersions":   srv.RunningVersions,
		"TaskStopper":       srv.TaskStopper,
		"Issues":            srv.Issues,
		"Events":            srv.Events,
		"Curators":          srv.Curators,
		"Memory":            srv.Memory,
		"Channels":          srv.Channels,
		"LogFeed":           srv.LogFeed,
		"TaskLogs":          srv.TaskLogs,
		"WorkRequests":      srv.WorkRequests,
		"Mappings":          srv.Mappings,
		"Bindings":          srv.Bindings,
		"Captures":          srv.Captures,
		"BindingDispatcher": srv.BindingDispatcher,
		"CaptureLimiter":    srv.CaptureLimiter,
	}
	for name, handle := range unwired {
		if !isNil(handle) {
			t.Errorf("%s is wired; it is a daemon-owned callback or runtime handle with no contract behind it yet", name)
		}
	}
	if srv.Workflows != nil {
		t.Error("Workflows is wired; the definition catalog is a filesystem scan on the daemon host")
	}
	if srv.UpdateReportPath != "" {
		t.Error("UpdateReportPath is set; host-local update relay stays with the daemon")
	}
}

// TestComposeLeavesCaptureIntakeUnset pins the seam rule: while the daemon's
// in-process dashboard is still running (its removal is bead
// archie-core-8cda.5.4), a second process wiring Captures/BindingDispatcher
// would give two listeners the same webhook intake authority, which
// docs/prds/ui-service-boundary.md:30-33 forbids. The route answers 503
// instead.
func TestComposeLeavesCaptureIntakeUnset(t *testing.T) {
	srv := compose(deps{Options: Options{}, Log: slog.New(slog.DiscardHandler)})
	if srv.Captures != nil || srv.BindingDispatcher != nil || srv.CaptureLimiter != nil {
		t.Fatal("capture intake is wired in the UI process; it must stay unset until the daemon's dashboard is removed (bead archie-core-8cda.5.4)")
	}
}
