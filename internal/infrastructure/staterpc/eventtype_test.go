package staterpc

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/samcharles93/archie-core/internal/domain/eventtype"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// fakeEventTypes records what crossed the wire and answers with err.
type fakeEventTypes struct {
	saved eventtype.EventType
	list  []eventtype.EventType
	err   error
}

func (f *fakeEventTypes) InsertEventType(_ context.Context, t eventtype.EventType) (string, error) {
	f.saved = t
	return "et-1", f.err
}

func (f *fakeEventTypes) UpdateEventType(_ context.Context, t eventtype.EventType) error {
	f.saved = t
	return f.err
}

func (f *fakeEventTypes) DeleteEventType(context.Context, string) error { return f.err }

func (f *fakeEventTypes) ListEventTypes(context.Context) ([]eventtype.EventType, error) {
	return f.list, f.err
}

func remoteEventTypes(t *testing.T, store storecontract.EventTypeStore) *Client {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	RegisterServer(server, Deps{EventTypes: store})
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

func TestEventTypeRoundTrip(t *testing.T) {
	stamp := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	want := eventtype.EventType{
		ID: "et-1", Source: "gh", Name: "pull_request",
		Rule: eventtype.Rule{
			Headers: []eventtype.HeaderCondition{{Name: "x-github-event", Value: "pull_request"}},
			Payload: []eventtype.PayloadCondition{{Path: "action", Op: eventtype.OpEquals, Value: "opened"}},
		},
		Schema:    map[string]eventtype.ValueType{"action": eventtype.TypeString},
		CreatedAt: stamp, UpdatedAt: stamp.Add(time.Hour),
	}
	fake := &fakeEventTypes{list: []eventtype.EventType{want}}
	c := remoteEventTypes(t, fake)

	got, err := c.ListEventTypes(t.Context())
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("ListEventTypes() = %+v (err %v), want %+v", got, err, want)
	}
	id, err := c.InsertEventType(t.Context(), want)
	if err != nil || id != "et-1" {
		t.Fatalf("InsertEventType() = %q, %v", id, err)
	}
	if !reflect.DeepEqual(fake.saved, want) {
		t.Fatalf("server received %+v, want %+v", fake.saved, want)
	}
}

func TestEventTypeErrorsCrossTheWire(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"overlap", eventtype.ErrOverlap},
		{"invalid", eventtype.ErrInvalid},
		{"not found", storecontract.ErrEventTypeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := remoteEventTypes(t, &fakeEventTypes{err: tt.err})
			if _, err := c.InsertEventType(t.Context(), eventtype.EventType{}); !errors.Is(err, tt.err) {
				t.Errorf("InsertEventType error = %v, want %v", err, tt.err)
			}
			if err := c.UpdateEventType(t.Context(), eventtype.EventType{}); !errors.Is(err, tt.err) {
				t.Errorf("UpdateEventType error = %v, want %v", err, tt.err)
			}
			if err := c.DeleteEventType(t.Context(), "x"); !errors.Is(err, tt.err) {
				t.Errorf("DeleteEventType error = %v, want %v", err, tt.err)
			}
		})
	}
}

func TestCapturedEventCarriesEventType(t *testing.T) {
	in := storecontract.CapturedEvent{ID: "c", Source: "gh", EventType: "et-1"}
	if got := capturedEventValue(capturedEventProto(in)); got.EventType != "et-1" {
		t.Fatalf("EventType after round trip = %q, want et-1", got.EventType)
	}
}
