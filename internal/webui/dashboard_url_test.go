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
