package webui

import "testing"

func TestDashboardURL(t *testing.T) {
	for _, tc := range []struct{ listen, token, want string }{
		{"0.0.0.0:8484", "abc", "http://localhost:8484/?t=abc"},
		{":8484", "abc", "http://localhost:8484/?t=abc"},
		{"[::]:8484", "abc", "http://localhost:8484/?t=abc"},
		{"127.0.0.1:8484", "", "http://127.0.0.1:8484/"},
		{"100.64.1.2:8484", "x", "http://100.64.1.2:8484/?t=x"},
	} {
		if got := DashboardURL(tc.listen, tc.token); got != tc.want {
			t.Errorf("DashboardURL(%q) = %q, want %q", tc.listen, got, tc.want)
		}
	}
}

// TestHealthURL pins what out-of-process tooling probes after restarting
// archied. A wrong address here reads as an unhealthy release and triggers a
// rollback of one that came up fine (archie-core-1r4g).
func TestHealthURL(t *testing.T) {
	for _, tc := range []struct{ listen, want string }{
		{"127.0.0.1:8484", "http://127.0.0.1:8484"},
		{"localhost:8484", "http://localhost:8484"},
		{"0.0.0.0:8484", "http://localhost:8484"},
		{":8484", "http://localhost:8484"},
		{"[::]:8484", "http://localhost:8484"},
		{"100.64.1.2:9000", "http://100.64.1.2:9000"},
		{"  127.0.0.1:8484  ", "http://127.0.0.1:8484"},
		{"off", ""},
		{"", ""},
	} {
		if got := HealthURL(tc.listen); got != tc.want {
			t.Errorf("HealthURL(%q) = %q, want %q", tc.listen, got, tc.want)
		}
	}
}
