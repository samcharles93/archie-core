package archied

import (
	"archive/tar"
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
)

// contributedLayer ships one workflow definition file's portable YAML.
func contributedLayer(t *testing.T, path, yaml string) []byte {
	t.Helper()
	var raw bytes.Buffer
	writer := tar.NewWriter(&raw)
	if err := writer.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: path, Size: int64(len(yaml))}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func packageDescriptor() storepkg.Descriptor {
	return storepkg.Descriptor{
		APIVersion: storepkg.APIVersion, DisplayName: "Review pack", Version: "1",
		Contributes: storepkg.Contributions{Workflows: []string{"workflows/review.yaml"}},
		Files:       []storepkg.File{{Path: "workflows/review.yaml", Mode: 0o644}},
	}
}

// TestPackageContributionsLiveRoundTrip drives the State Store's own
// composition — the projections stateStoreDeps wires, the install and remove
// RPCs the dashboard calls, and the workflow-definitions resource the daemon
// reads per dispatch — over one real gRPC connection: installing a package
// makes its workflow a usable org resource, and removing it stops being
// usable, both without restarting anything.
func TestPackageContributionsLiveRoundTrip(t *testing.T) {
	db := pgstore.Open(t)
	projections, err := newPackageProjections(db.Pool)
	if err != nil {
		t.Fatal(err)
	}
	const yaml = "id: review\nsteps:\n  - type: bootstrap.apply\n"
	service := storepkg.Service{
		Registry:    packageLayerRegistry{descriptor: packageDescriptor(), layer: contributedLayer(t, "workflows/review.yaml", yaml)},
		Store:       postgres.NewInstalledPackages(db.Pool),
		Projections: projections,
	}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	staterpc.RegisterServer(server, staterpc.Deps{Packages: service})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///state",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := staterpc.NewClient(conn)
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	if _, err := client.InstallPackage(t.Context(), "org-a", "review", "localhost:5000/review", digest); err != nil {
		t.Fatal(err)
	}
	// The contribution is usable: the org resource the daemon's
	// WorkflowDefinitions client reads carries it now, no restart involved.
	resource, err := postgres.NewResources(db.Pool).Resource(t.Context(), "org-a", controlplane.WorkflowDefinitionsKind)
	if err != nil {
		t.Fatal(err)
	}
	collection, err := workflow.DecodeDefinitionCollection(resource.Value, workflow.BuiltinStepRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := collection.DefinitionByID("review"); !ok {
		t.Fatal("installed package's workflow is not an org workflow after install")
	}
	if err := client.RemoveInstalled(t.Context(), "org-a", "review"); err != nil {
		t.Fatal(err)
	}
	resource, err = postgres.NewResources(db.Pool).Resource(t.Context(), "org-a", controlplane.WorkflowDefinitionsKind)
	if err != nil {
		t.Fatal(err)
	}
	collection, err = workflow.DecodeDefinitionCollection(resource.Value, workflow.BuiltinStepRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := collection.DefinitionByID("review"); ok {
		t.Fatal("removed package's workflow still dispatches: the org resource kept it")
	}
	if entries, err := postgres.NewPackageContributions(db.Pool).Entries(t.Context(), "org-a", "review"); err != nil || len(entries) != 0 {
		t.Fatalf("contributions ledger after removal = %#v, %v", entries, err)
	}
}

type packageLayerRegistry struct {
	descriptor storepkg.Descriptor
	layer      []byte
}

func (r packageLayerRegistry) Fetch(context.Context, string, string) (storepkg.Descriptor, []byte, error) {
	return r.descriptor, r.layer, nil
}

// TestStateStoreDepsWiresPackageProjections pins the wiring the live round
// trip drives: stateStoreDeps builds projections on the State Store's pool and
// carries them into the packages service it composes.
func TestStateStoreDepsWiresPackageProjections(t *testing.T) {
	file := "state_store.go"
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	ast.Inspect(parsed, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if fn, ok := call.Fun.(*ast.Ident); ok {
				calls = append(calls, fn.Name)
			}
		}
		return true
	})
	found := false
	for _, call := range calls {
		if call == "newPackageProjections" {
			found = true
		}
	}
	if !found {
		t.Fatalf("stateStoreDeps (%s body) wires no package projections", file)
	}
}
