package archied

import "testing"

// TestResolveServiceListen pins the precedence: an explicit -listen wins, else
// the configured address, and an address is never invented.
//
// The error cases are the ones that matter. net.Listen reads "" as "any free
// port", so defaulting here would bind the service somewhere its clients do not
// look and report success while doing it -- the exact failure this helper exists
// to make impossible.
func TestResolveServiceListen(t *testing.T) {
	for _, tt := range []struct {
		name, flagValue, configured, want string
		wantErr                           bool
	}{
		{
			name:      "the flag wins over the configured address",
			flagValue: "127.0.0.1:9191", configured: "127.0.0.1:9090",
			want: "127.0.0.1:9191",
		},
		{
			name:       "the configured address is used when no flag was passed",
			configured: "127.0.0.1:9191",
			want:       "127.0.0.1:9191",
		},
		{
			name:      "a whitespace flag is not an address",
			flagValue: "   ", configured: "127.0.0.1:9191",
			want: "127.0.0.1:9191",
		},
		{
			name:    "both empty is an error, never a silent ephemeral port",
			wantErr: true,
		},
		{
			name:      "whitespace on both sides is an error too",
			flagValue: " ", configured: "\t",
			wantErr: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveServiceListen("state", tt.flagValue, tt.configured)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("err = nil, want an error; got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("listen = %q, want %q", got, tt.want)
			}
		})
	}
}
