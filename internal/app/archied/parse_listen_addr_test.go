package archied

import "testing"

func TestParseListenAddrPreservesEphemeralPort(t *testing.T) {
	host, port := parseListenAddr("127.0.0.1:0", "0.0.0.0", 8644)
	if host != "127.0.0.1" || port != 0 {
		t.Fatalf("parseListenAddr() = (%q, %d), want (%q, %d)", host, port, "127.0.0.1", 0)
	}
}
