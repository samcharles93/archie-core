package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	dockercontainer "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/samcharles93/archie-core/internal/config"
	archiecontainer "github.com/samcharles93/archie-core/internal/container"
)

// This pins the last handoff seam, not just its two halves: the runtime NATS
// endpoint frozen in Daemon becomes the environment on a container created on
// the same Docker network whose gateway embedded NATS binds.
func TestConnectedNATSEndpointReachesManagedContainerCreate(t *testing.T) {
	const (
		networkName = "workers"
		gateway     = "172.31.0.1"
		containerID = "12345678901234567890123456789012"
		token       = "runtime-only-token"
	)
	var createRequest dockercontainer.CreateRequest
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/networks/"+networkName):
			writeContainerDockerJSON(t, w, map[string]any{
				"Name":   networkName,
				"Driver": "bridge",
				"Scope":  "local",
				"IPAM": map[string]any{
					"Config": []map[string]string{{
						"Subnet":  "172.31.0.0/16",
						"Gateway": gateway,
					}},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			if err := json.NewDecoder(r.Body).Decode(&createRequest); err != nil {
				t.Errorf("decode container create request: %v", err)
			}
			writeContainerDockerJSON(t, w, dockercontainer.CreateResponse{ID: containerID})
		case strings.HasSuffix(r.URL.Path, "/containers/"+containerID+"/start"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			writeContainerDockerJSON(t, w, []any{})
		default:
			http.Error(w, fmt.Sprintf("unexpected Docker API path %s", r.URL.Path), http.StatusNotFound)
		}
	}))
	t.Cleanup(dockerAPI.Close)

	dockerClient, err := client.New(client.WithHost(dockerAPI.URL), client.WithAPIVersion("1.55"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dockerClient.Close() })

	pool, err := archiecontainer.NewPool(t.Context(), archiecontainer.Config{
		Image:              "archie-agent:test",
		Network:            networkName,
		DockerClient:       dockerClient,
		RequireHostGateway: true,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("container.NewPool = %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	d := &Daemon{
		Cfg: config.NewHolder(config.Config{}),
		ConnectedNATS: NATSEndpoint{
			URL:   "nats://" + gateway + ":4222",
			Token: token,
		},
	}
	if _, err := pool.Acquire(t.Context(), nil, d.containerEnv(nil)); err != nil {
		t.Fatalf("Pool.Acquire = %v", err)
	}

	if createRequest.HostConfig == nil || string(createRequest.HostConfig.NetworkMode) != networkName {
		t.Fatalf("container network = %v, want %q", createRequest.HostConfig, networkName)
	}
	for _, want := range []string{
		"NATS_URL=nats://" + gateway + ":4222",
		"NATS_TOKEN=" + token,
	} {
		if !slices.Contains(createRequest.Env, want) {
			t.Errorf("container env = %q, want %q", createRequest.Env, want)
		}
	}
}

func writeContainerDockerJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode Docker response: %v", err)
	}
}
