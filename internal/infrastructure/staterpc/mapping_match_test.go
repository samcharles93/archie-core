package staterpc

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
)

type fakeMappingMatches struct{ mappingID, captureID string }

func (f *fakeMappingMatches) RecordMappingMatch(_ context.Context, mappingID, captureID string) error {
	f.mappingID, f.captureID = mappingID, captureID
	return nil
}

func TestRecordMappingMatchCrossesTheWire(t *testing.T) {
	fake := &fakeMappingMatches{}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	RegisterServer(server, Deps{MappingMatches: fake})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///state",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := NewClient(conn).RecordMappingMatch(t.Context(), "m1", "c1"); err != nil {
		t.Fatalf("RecordMappingMatch() error = %v", err)
	}
	if fake.mappingID != "m1" || fake.captureID != "c1" {
		t.Fatalf("server received (%q, %q), want (m1, c1)", fake.mappingID, fake.captureID)
	}
}

func TestMappingAndBindingCarryEventTypeCountsAndFilter(t *testing.T) {
	stamp := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	m := mapping.Mapping{ID: "m", Name: "n", Fields: []mapping.Field{{Name: "a", Path: "a", Type: mapping.TypeString}}, EventTypeID: "et-1", MatchCount: 7, LastMatchedAt: stamp, CreatedAt: stamp, UpdatedAt: stamp}
	if got := mappingValue(mappingProto(m)); !reflect.DeepEqual(got, m) {
		t.Fatalf("mapping after round trip = %+v, want %+v", got, m)
	}
	b := binding.Binding{ID: "b", Filter: `severity == "high"`, CreatedAt: stamp, UpdatedAt: stamp}
	if got := bindingValue(bindingProto(b)); got.Filter != b.Filter {
		t.Fatalf("binding filter after round trip = %q, want %q", got.Filter, b.Filter)
	}
}
