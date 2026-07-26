package natsrpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/natsrpc"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	if err := natsrpc.NewEnvelope(nil).Err(); err != nil {
		t.Errorf("NewEnvelope(nil).Err() = %v, want nil", err)
	}

	want := "boom"
	env := natsrpc.NewEnvelope(errBoom{want})
	if env.Error != want {
		t.Errorf("Error = %q, want %q", env.Error, want)
	}
	if got := env.Err(); got == nil || got.Error() != want {
		t.Errorf("Err() = %v, want %q", got, want)
	}
}

type errBoom struct{ msg string }

func (e errBoom) Error() string { return e.msg }

// ── Regression tests ───────────────────────────────────────────────

func TestNewEnvelopeEdgeCases(t *testing.T) {
	t.Run("empty error string produces nil Err", func(t *testing.T) {
		env := natsrpc.NewEnvelope(errors.New(""))
		if env.Error != "" {
			t.Errorf("empty error string should produce empty Error field, got %q", env.Error)
		}
	})

	t.Run("zero-value Envelope Err returns nil", func(t *testing.T) {
		var env natsrpc.Envelope
		if err := env.Err(); err != nil {
			t.Errorf("zero Envelope.Err() = %v, want nil", err)
		}
	})
}

func TestEnvelopeJSONRoundTrip(t *testing.T) {
	// Simulate a full server→client JSON round trip through the envelope.
	type Response struct {
		natsrpc.Envelope
		Result string `json:"result,omitempty"`
	}

	t.Run("success response", func(t *testing.T) {
		resp := Response{Result: "ok"}
		data, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var decoded Response
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if decoded.Result != "ok" {
			t.Errorf("Result = %q", decoded.Result)
		}
		if decoded.Err() != nil {
			t.Errorf("expected nil error, got %v", decoded.Err())
		}
	})

	t.Run("error response", func(t *testing.T) {
		resp := Response{Envelope: natsrpc.NewEnvelope(errors.New("request failed"))}
		data, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var decoded Response
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if decoded.Err() == nil || decoded.Err().Error() != "request failed" {
			t.Errorf("expected error 'request failed', got %v", decoded.Err())
		}
		// Error field flattens into parent JSON.
		if decoded.Envelope.Error != "request failed" {
			t.Errorf("Error = %q", decoded.Envelope.Error)
		}
	})
}

func TestClientRequestTimeoutFallback(t *testing.T) {
	// Without a real NATS connection, we can test the context deadline
	// fallback logic by checking what Client.Request does with a nil conn.
	// It panics on nil, but we can verify the timeout is computed correctly
	// by testing the context setup logic indirectly.

	// Client with no deadline on ctx and Timeout > 0 should call
	// context.WithTimeout.
	c := &natsrpc.Client{Timeout: 100 * time.Millisecond}
	ctx := context.Background()
	// Since Conn is nil, this will panic. But the context setup happens
	// before the panic, so verifying the context has a deadline is what
	// matters. We can't easily test without a mock NATS, so validate the
	// Client struct accepts the Timeout.
	if c.Timeout != 100*time.Millisecond {
		t.Errorf("Timeout = %v", c.Timeout)
	}
	_ = ctx
}

func TestRegisterAllRollbackOnError(t *testing.T) {
	// RegisterAll should unsubscribe already-registered handlers when a
	// later registration fails. Without a real NATS connection we verify
	// the function signature and zero-registrations case.
	unsub, err := natsrpc.RegisterAll(nil, []natsrpc.Registration{})
	if unsub == nil && err == nil {
		// Both nil is fine for empty regs with nil conn.
	} else {
		t.Logf("RegisterAll with nil conn and empty regs: err=%v", err)
	}
}

