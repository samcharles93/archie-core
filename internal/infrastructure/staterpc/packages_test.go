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
