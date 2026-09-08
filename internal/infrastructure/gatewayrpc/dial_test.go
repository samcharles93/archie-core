package gatewayrpc

import "testing"

func TestTargetIsLoopback(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want bool
	}{
		{"loopback ipv4", "127.0.0.1:8585", true},
		{"loopback ipv6", "[::1]:8585", true},
		{"localhost hostname", "localhost:8585", true},
		{"localhost uppercase", "LOCALHOST:8585", true},
		{"wildcard", "0.0.0.0:8585", false},
		{"private ip", "192.168.1.10:8585", false},
		{"dns name", "gateway.example.com:8585", false},
		{"empty host", ":8585", false},
		{"malformed", "127.0.0.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TargetIsLoopback(tt.addr)
			if tt.name == "malformed" {
				if err == nil {
					t.Fatalf("TargetIsLoopback(%q) = (%v, nil), want error", tt.addr, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("TargetIsLoopback(%q): %v", tt.addr, err)
			}
			if got != tt.want {
				t.Fatalf("TargetIsLoopback(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

// TestDialFailsClosedOffLoopbackWithoutToken is the transport half of the
// Gateway security rule: a target reachable from the network must present a
// Bearer [REDACTED], so a missing credential stops the dial rather than opening
// an unauthenticated connection. It mirrors staterpc's §9 dial rule.
func TestDialFailsClosedOffLoopbackWithoutToken(t *testing.T) {
	if _, _, err := Dial("", ""); err == nil {
		t.Fatal("Dial with an empty target should fail")
	}
	if _, _, err := Dial("0.0.0.0:8585", ""); err == nil {
		t.Fatal("Dial to a non-loopback target without a token should fail closed")
	}
	client, cleanup, err := Dial("0.0.0.0:8585", "secret")
	if err != nil {
		t.Fatalf("Dial to a non-loopback target with a token: %v", err)
	}
	defer cleanup()
	if client == nil {
		t.Fatal("Dial returned no client")
	}
	loopback, cleanupLoopback, err := Dial("127.0.0.1:8585", "")
	if err != nil {
		t.Fatalf("Dial to a loopback target without a token: %v", err)
	}
	defer cleanupLoopback()
	if loopback == nil {
		t.Fatal("Dial returned no client for a loopback target")
	}
}
