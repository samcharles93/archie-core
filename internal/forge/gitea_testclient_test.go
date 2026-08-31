package forge

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"code.gitea.io/sdk/gitea"
)

// newTestGiteaClient wires a GiteaClient at a local httptest server so its
// methods can be exercised without hitting a real Gitea instance. The
// caller's mux handles every route except /api/v1/version, which this
// registers so client construction (which probes it) succeeds.
func newTestGiteaClient(t *testing.T) (*GiteaClient, *http.ServeMux) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]string{"version": "1.21.0"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cli, err := gitea.NewClient(srv.URL, gitea.SetToken("token"))
	if err != nil {
		t.Fatal(err)
	}

	return &GiteaClient{cli: cli, host: srv.URL, token: "token", log: slog.New(slog.DiscardHandler)}, mux
}
