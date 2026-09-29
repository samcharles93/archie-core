package staterpc

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
)

type packageRegistry struct{ descriptor storepkg.Descriptor }

func (r packageRegistry) Fetch(context.Context, string, string) (storepkg.Descriptor, []byte, error) {
	return r.descriptor, []byte("package layer"), nil
}

func TestInstalledPackagesCrossStateStoreWire(t *testing.T) {
	db := pgstore.Open(t)
	service := storepkg.Service{
		Registry: packageRegistry{descriptor: storepkg.Descriptor{
			APIVersion: storepkg.APIVersion, DisplayName: "Review", Version: "1",
			Contributes: storepkg.Contributions{Workflows: []string{"review"}},
		}},
		Store: postgres.NewInstalledPackages(db.Pool),
	}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	RegisterServer(server, Deps{Packages: service})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///state",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := NewClient(conn)
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	installed, err := client.InstallPackage(t.Context(), "org-a", "review", "localhost:5000/review", digest)
	if err != nil {
		t.Fatal(err)
	}
	if installed.Digest != digest || string(installed.Layer) != "package layer" {
		t.Fatalf("install = %#v", installed)
	}
	if _, err := client.InstallPackage(t.Context(), "org-a", "review", "localhost:5000/review", digest); !errors.Is(err, storepkg.ErrInstalled) {
		t.Fatalf("duplicate install error = %v", err)
	}
	list, err := client.ListInstalled(t.Context(), "org-a")
	if err != nil || len(list) != 1 || len(list[0].Layer) != 0 {
		t.Fatalf("list = %#v, %v", list, err)
	}
	if other, err := client.ListInstalled(t.Context(), "org-b"); err != nil || len(other) != 0 {
		t.Fatalf("other org list = %#v, %v", other, err)
	}
	if err := client.RemoveInstalled(t.Context(), "org-a", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetInstalled(t.Context(), "org-a", "review"); !errors.Is(err, storepkg.ErrNotFound) {
		t.Fatalf("removed package get error = %v", err)
	}
}

// TestPackageAuthorityCrossStateStoreWire drives the acceptance record over a
// real gRPC connection against a real database: the record the operator
// accepts rides the wire, persists against the pinned digest, and a grant
// declared beyond the record is refused end to end
// (docs/prds/store.md, "Authority").
func TestPackageAuthorityCrossStateStoreWire(t *testing.T) {
	declared := storepkg.Descriptor{
		APIVersion: storepkg.APIVersion, DisplayName: "Review", Version: "1",
		Contributes: storepkg.Contributions{Workflows: []string{"review"}},
		Authority:   storepkg.Authority{ForgePermissions: []string{"read", "comment"}, Tools: []string{"shell"}},
	}
	db := pgstore.Open(t)
	service := storepkg.Service{
		Registry: packageRegistry{descriptor: declared},
		Store:    postgres.NewInstalledPackages(db.Pool),
	}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	RegisterServer(server, Deps{Packages: service})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///state",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := NewClient(conn)
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := client.InstallPackage(t.Context(), "org-a", "review", "localhost:5000/review", digest); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.RemoveInstalled(t.Context(), "org-a", "review") })

	installed, err := client.GetInstalled(t.Context(), "org-a", "review")
	if err != nil {
		t.Fatal(err)
	}
	if installed.AcceptedAuthority != nil {
		t.Fatalf("fresh install already accepted: %#v", installed.AcceptedAuthority)
	}

	// The operator accepts a narrowed subset: the record is the bound the
	// package is later checked against, not the full declaration.
	accepted := storepkg.Authority{ForgePermissions: []string{"read"}}
	if _, err := client.AcceptPackageAuthority(t.Context(), "org-a", "review", accepted); err != nil {
		t.Fatal(err)
	}
	for _, got := range []struct {
		name string
		pkg  storepkg.Installed
	}{{"get", mustGetInstalled(t, client, "org-a", "review")}} {
		if got.pkg.AcceptedAuthority == nil || !got.pkg.AcceptedAuthority.Covers(accepted) {
			t.Fatalf("%s record lost the acceptance: %#v", got.name, got.pkg.AcceptedAuthority)
		}
	}
	list, err := client.ListInstalled(t.Context(), "org-a")
	if err != nil || len(list) != 1 || list[0].AcceptedAuthority == nil || !list[0].AcceptedAuthority.Covers(accepted) {
		t.Fatalf("list lost the acceptance: %#v, %v", list, err)
	}

	// A grant the package never declares is not an acceptance of that
	// package: the wire preserves the sentinel so the caller sees one error.
	if _, err := client.AcceptPackageAuthority(t.Context(), "org-a", "review", storepkg.Authority{ForgePermissions: []string{"read", "push"}}); !errors.Is(err, storepkg.ErrAuthorityNotDeclared) {
		t.Fatalf("undeclared grant error = %v", err)
	}
	if _, err := client.AcceptPackageAuthority(t.Context(), "org-a", "missing", accepted); !errors.Is(err, storepkg.ErrNotFound) {
		t.Fatalf("unknown package error = %v", err)
	}
}

func mustGetInstalled(t *testing.T, client *Client, org, name string) storepkg.Installed {
	t.Helper()
	installed, err := client.GetInstalled(t.Context(), org, name)
	if err != nil {
		t.Fatal(err)
	}
	return installed
}
