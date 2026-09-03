package archied

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/client"
	natsio "github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/container"
)

// setupContainers must discover the bridge before embedded NATS starts, but
// its cleanup registration comes afterwards. boot.cleanup is LIFO, so this
// ordering stops worker containers before closing the daemon client and the
// embedded server they are connected to.
func TestSetupBackendsRegistersContainerCleanupAfterNATS(t *testing.T) {
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, "bootstrap.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	setupContainers := methodBody(t, file, "setupContainers")
	if methodCallPosition(setupContainers, "addCleanup") != token.NoPos {
		t.Fatal("setupContainers registers cleanup before NATS; LIFO would stop the broker before workers")
	}
	setupBackends := methodBody(t, file, "setupBackends")
	connect := methodCallPosition(setupBackends, "connectNATS")
	register := methodCallPosition(setupBackends, "addCleanup")
	if connect == token.NoPos || register == token.NoPos || register < connect {
		t.Fatalf("setupBackends call positions: connectNATS=%d addCleanup=%d, want container cleanup registered after connect", connect, register)
	}
}

func methodBody(t *testing.T, file *ast.File, name string) *ast.BlockStmt {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv != nil && fn.Name.Name == name {
			return fn.Body
		}
	}
	t.Fatalf("method %s not found", name)
	return nil
}

func methodCallPosition(body *ast.BlockStmt, name string) token.Pos {
	position := token.NoPos
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == name && position == token.NoPos {
			position = call.Pos()
		}
		return true
	})
	return position
}

// This is the native-daemon deployment seam: Docker supplies the host-side
// gateway of the worker bridge, embedded NATS binds that address, and the
// runtime credential shared with workers is required by the same listener.
func TestConnectEmbeddedNATSUsesManagedWorkerBridge(t *testing.T) {
	const gateway = "127.0.0.1"
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/networks/workers"):
			writeDockerResponse(t, w, map[string]any{
				"Name":   "workers",
				"Driver": "bridge",
				"Scope":  "local",
				"IPAM": map[string]any{
					"Config": []map[string]string{{
						"Subnet":  "127.0.0.0/8",
						"Gateway": gateway,
					}},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			writeDockerResponse(t, w, []any{})
		default:
			http.Error(w, fmt.Sprintf("unexpected Docker API path %s", r.URL.Path), http.StatusNotFound)
		}
	}))
	t.Cleanup(dockerAPI.Close)

	dockerClient, err := client.New(client.WithHost(dockerAPI.URL), client.WithAPIVersion("1.55"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dockerClient.Close() })

	pool, err := container.NewPool(t.Context(), container.Config{
		DockerClient:       dockerClient,
		Network:            "workers",
		RequireHostGateway: true,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("container.NewPool = %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	b := &boot{
		cfg: config.Config{
			DBPath: filepath.Join(t.TempDir(), "archie.db"),
			NATS:   config.NATSConfig{Mode: config.NATSModeEmbedded},
		},
		log:           slog.New(slog.DiscardHandler),
		containerPool: pool,
	}
	if err := b.connectNATS(t.Context()); err != nil {
		t.Fatalf("connectNATS = %v", err)
	}
	t.Cleanup(b.cleanup)

	if !strings.HasPrefix(b.natsURL, "nats://"+gateway+":") {
		t.Errorf("connected NATS URL = %q, want bridge gateway %s", b.natsURL, gateway)
	}
	if b.natsToken == "" {
		t.Fatal("connected NATS token is empty")
	}
	if unauthenticated, connectErr := natsio.Connect(b.natsURL); connectErr == nil {
		unauthenticated.Close()
		t.Fatal("unauthenticated worker connection succeeded")
	}
	authenticated, err := natsio.Connect(b.natsURL, natsio.Token(b.natsToken))
	if err != nil {
		t.Fatalf("authenticated worker connection = %v", err)
	}
	authenticated.Close()
}

func writeDockerResponse(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode Docker response: %v", err)
	}
}

func TestConnectExternalNATSEmptyURLReturnsConfigurationError(t *testing.T) {
	b := newBootstrap()
	b.cfg.NATS = config.NATSConfig{Mode: config.NATSModeExternal}

	err := b.connectNATS(t.Context())
	if err == nil || !strings.Contains(err.Error(), "nats.url is required") {
		t.Fatalf("connectNATS() error = %v, want useful missing-url error", err)
	}
}

func TestSetupObservabilityDoesNotTouchDaemonBeforeBuild(t *testing.T) {
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, "bootstrap.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	setup := methodBody(t, file, "setupObservability")
	var daemonAssignments int
	ast.Inspect(setup, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range assign.Lhs {
			if selector, ok := lhs.(*ast.SelectorExpr); ok {
				if receiver, ok := selector.X.(*ast.SelectorExpr); ok && receiver.Sel.Name == "d" {
					daemonAssignments++
				}
			}
		}
		return true
	})
	if daemonAssignments != 0 {
		t.Fatalf("setupObservability assigns daemon fields %d times; daemon is built later", daemonAssignments)
	}
}
