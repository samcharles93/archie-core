package nats

import "testing"

// TestClientConnectedReflectsTheLiveConnection pins the broker fact /status
// reports. It must answer from the connection the daemon already holds -- a
// client that dialled the broker itself would report whether a fresh dial
// works, not whether this process's own task bus is up, and would put a
// network round trip (and its timeout) inside a chat command's handler.
func TestClientConnectedReflectsTheLiveConnection(t *testing.T) {
	ctx := t.Context()
	srv, err := StartEmbedded(ctx, EmbeddedOptions{StoreDir: t.TempDir()}, discardLogger())
	if err != nil {
		t.Fatalf("StartEmbedded = %v", err)
	}
	t.Cleanup(srv.Shutdown)

	live, err := Connect(ctx, Config{
		URL:           srv.ClientURL(),
		Token:         srv.Token(),
		Subjects:      []string{"status.test.>"},
		FilterSubject: "status.test.>",
	}, discardLogger())
	if err != nil {
		t.Fatalf("Connect = %v", err)
	}
	t.Cleanup(live.Close)

	closed, err := Connect(ctx, Config{
		URL:           srv.ClientURL(),
		Token:         srv.Token(),
		Subjects:      []string{"status.closed.>"},
		FilterSubject: "status.closed.>",
	}, discardLogger())
	if err != nil {
		t.Fatalf("Connect = %v", err)
	}
	closed.Close()

	tests := []struct {
		name   string
		client *Client
		want   bool
	}{
		{name: "connected client", client: live, want: true},
		{name: "closed client", client: closed, want: false},
		{name: "no client at all", client: nil, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.client.Connected(); got != tc.want {
				t.Errorf("Connected() = %v, want %v", got, tc.want)
			}
		})
	}
}
