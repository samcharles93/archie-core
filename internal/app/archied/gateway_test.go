package archied

import "testing"

func TestGatewayServerOptsLoopbackIsInsecure(t *testing.T) {
	opts, loopback, err := gatewayServerOpts("127.0.0.1:8585", "")
	if err != nil {
		t.Fatalf("gatewayServerOpts(loopback, no token) error: %v", err)
	}
	if !loopback {
		t.Fatal("loopback listener should report loopback")
	}
	if len(opts) != 0 {
		t.Fatalf("loopback listener should have no server opts, got %d", len(opts))
	}
}

func TestGatewayServerOptsNonLoopbackRequiresToken(t *testing.T) {
	if _, _, err := gatewayServerOpts("0.0.0.0:8585", ""); err == nil {
		t.Fatal("non-loopback listener without a token should fail closed")
	}
	opts, loopback, err := gatewayServerOpts("0.0.0.0:8585", "secret")
	if err != nil {
		t.Fatalf("non-loopback listener with a token error: %v", err)
	}
	if loopback {
		t.Fatal("non-loopback listener should not report loopback")
	}
	if len(opts) != 2 {
		t.Fatalf("non-loopback listener should install two server options (unary and stream auth interceptors), got %d", len(opts))
	}
}

func TestGatewayServerOptsMalformedListen(t *testing.T) {
	if _, _, err := gatewayServerOpts("not-an-address", ""); err == nil {
		t.Fatal("malformed listen address should error")
	}
}
