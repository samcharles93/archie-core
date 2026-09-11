package archieui

import (
	"log/slog"
	"reflect"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
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
// daemon's live config.Holder". webui.Server no longer has a holder field to
// receive (archie-core-ml30), so that clause is now enforced by the type
// rather than asserted here. What remains to pin is the rest of the
// composition: the two contract-backed fields hold remote clients, and every
// daemon-local runtime handle stays nil so the process cannot hold in-process
// daemon state by accident (docs/prds/ui-service-boundary.md, "Boundary and
// ownership").
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
		"UpdateConfig":    srv.UpdateConfig,
		"ResetConfig":     srv.ResetConfig,
		"UpdateRepoField": srv.UpdateRepoField,
		"ReloadChannel":   srv.ReloadChannel,
		"RunningVersions": srv.RunningVersions,
		"Events":          srv.Events,
		"Curators":        srv.Curators,
		"Memory":          srv.Memory,
		"Channels":        srv.Channels,
		"LogFeed":         srv.LogFeed,
		"TaskLogs":        srv.TaskLogs,
		"WorkRequests":    srv.WorkRequests,
	}
	for name, handle := range unwired {
		if !isNil(handle) {
			t.Errorf("%s is wired; it is a daemon-owned callback or runtime handle with no contract behind it yet", name)
		}
	}
	if srv.Workflows != nil {
		t.Error("Workflows is wired; the definition catalog is a filesystem scan on the daemon host")
	}
	// Mappings and bindings are the other way round: ratified State Store
	// contracts, carried by the client this process already holds, so
	// leaving them nil would degrade two pages that have an owner.
	if srv.Mappings == nil || srv.Bindings == nil {
		t.Error("Mappings/Bindings are unwired; both are State Store contracts the composed client implements")
	}
	if srv.UpdateReportPath != "" {
		t.Error("UpdateReportPath is set; host-local update relay stays with the daemon")
	}
}

// TestComposeWiresCaptureIntake pins the cutover rule: once the daemon's
// dashboard listener is gone (archie-core-8cda.5.4), this process is the
// only listener serving POST /webhooks/capture/{source}, so compose must
// mount the receiver when the store contract carries it -- a capture POST
// answered by a token-gated 404 would leave intake with no owner. Zero
// Options.Capture falls back to configuration.DefaultCapture, the same
// defaults a decoded config would project.
func TestComposeWiresCaptureIntake(t *testing.T) {
	stateConn, err := grpc.NewClient("passthrough:///127.0.0.1:1", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("state client: %v", err)
	}
	defer stateConn.Close()

	srv := compose(deps{
		Options: Options{},
		Log:     slog.New(slog.DiscardHandler),
		Store:   staterpc.NewClient(stateConn),
	})
	if srv.Captures == nil || srv.CaptureIntake == nil {
		t.Fatal("capture intake is unset; the UI process is the only listener serving POST /webhooks/capture/{source} after the cutover (bead archie-core-8cda.5.4)")
	}
	defaults := configuration.DefaultCapture()
	if srv.CaptureMaxEvents != defaults.MaxEvents {
		t.Errorf("CaptureMaxEvents = %d, want the default %d", srv.CaptureMaxEvents, defaults.MaxEvents)
	}

	// A store without the capture contract keeps both surfaces unset and
	// degrades the read instead of mounting an intake route that cannot
	// persist what arrives.
	empty := compose(deps{Options: Options{}, Log: slog.New(slog.DiscardHandler)})
	if empty.Captures != nil || empty.CaptureIntake != nil || empty.CaptureMaxEvents != 0 {
		t.Error("capture surfaces wired without a CaptureStore behind them")
	}
}
