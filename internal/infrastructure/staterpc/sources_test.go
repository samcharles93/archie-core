package staterpc

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/domain/source"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// memSources is an in-memory SourceStore with the Postgres store's error
// contract, so the wire test checks the adapter rather than a database.
type memSources map[string]source.Source

func (m memSources) InsertSource(_ context.Context, s source.Source) error {
	if _, ok := m[s.Path]; ok {
		return storecontract.ErrSourcePathTaken
	}
	m[s.Path] = s
	return nil
}

func (m memSources) GetSource(_ context.Context, path string) (*source.Source, error) {
	s, ok := m[path]
	if !ok {
		return nil, nil
	}
	return &s, nil
}

func (m memSources) ListSources(context.Context) ([]source.Source, error) {
	out := make([]source.Source, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	return out, nil
}

func (m memSources) SetSourceSigning(_ context.Context, path string, from, to source.Signing) error {
	s, ok := m[path]
	switch {
	case !ok:
		return storecontract.ErrSourceNotFound
	case s.Signing != from:
		return storecontract.ErrSourceSigningStale
	}
	s.Signing = to
	m[path] = s
	return nil
}

func (m memSources) SetSourceSecret(_ context.Context, path, secret string) error {
	s, ok := m[path]
	if !ok {
		return storecontract.ErrSourceNotFound
	}
	s.Secret = secret
	m[path] = s
	return nil
}

func remoteSources(t *testing.T, sources storecontract.SourceStore) *Client {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	RegisterServer(server, Deps{Sources: sources})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///state",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return NewClient(conn)
}

func TestSourceStoreOverTheWire(t *testing.T) {
	ctx := t.Context()
	c := remoteSources(t, memSources{})
	stamp := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	in := source.Source{Path: "sentry", Signing: source.SigningSigned, Secret: "k", CreatedAt: stamp, UpdatedAt: stamp}
	if err := c.InsertSource(ctx, in); err != nil {
		t.Fatalf("InsertSource() error = %v", err)
	}
	got, err := c.GetSource(ctx, "sentry")
	if err != nil || got == nil || *got != in {
		t.Fatalf("GetSource() = %+v, %v; want %+v", got, err, in)
	}
	if missing, err := c.GetSource(ctx, "nope"); err != nil || missing != nil {
		t.Fatalf("GetSource(unknown) = %+v, %v; want nil, nil", missing, err)
	}
	if err := c.SetSourceSigning(ctx, "sentry", source.SigningSigned, source.SigningUnsignedPending); err != nil {
		t.Fatalf("SetSourceSigning() error = %v", err)
	}
	if err := c.SetSourceSecret(ctx, "sentry", "k2"); err != nil {
		t.Fatalf("SetSourceSecret() error = %v", err)
	}
	list, err := c.ListSources(ctx)
	if err != nil || len(list) != 1 || list[0].Signing != source.SigningUnsignedPending || list[0].Secret != "k2" {
		t.Fatalf("ListSources() = %+v, %v", list, err)
	}

	errs := []struct {
		name string
		call func() error
		want error
	}{
		{"taken path", func() error { return c.InsertSource(ctx, in) }, storecontract.ErrSourcePathTaken},
		{"stale signing", func() error {
			return c.SetSourceSigning(ctx, "sentry", source.SigningSigned, source.SigningUnsigned)
		}, storecontract.ErrSourceSigningStale},
		{"unknown path", func() error { return c.SetSourceSecret(ctx, "nope", "k") }, storecontract.ErrSourceNotFound},
	}
	for _, tt := range errs {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestSourceStoreUnavailableWithoutBackend(t *testing.T) {
	c := remoteSources(t, nil)
	_, err := c.ListSources(t.Context())
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("ListSources() with no backend = %v, want Unavailable", err)
	}
}

func TestCapturedEventUnsignedSurvivesTheWire(t *testing.T) {
	in := storecontract.CapturedEvent{ID: "c", Source: "fw", Unsigned: true}
	if got := capturedEventValue(capturedEventProto(in)); got != in {
		t.Fatalf("round trip = %+v, want %+v", got, in)
	}
}
