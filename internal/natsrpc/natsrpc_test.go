package natsrpc_test

import (
	"testing"

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
