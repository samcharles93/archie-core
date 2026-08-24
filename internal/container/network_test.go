package container

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/moby/moby/client"
)

// A native archied process is not itself attached to a Docker network. When
// embedded NATS must be reachable by managed workers, the pool therefore uses
// Docker's default bridge and exposes its host-side gateway to composition.
func TestNewPoolResolvesDefaultBridgeGatewayForNativeDaemon(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	const gateway = "172.31.0.1"

	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/"+hostname+"/json"):
			http.Error(w, `{"message":"no such container"}`, http.StatusNotFound)
		case strings.HasSuffix(r.URL.Path, "/networks/bridge"):
			writeDockerJSON(t, w, map[string]any{
				"Name":   "bridge",
				"Driver": "bridge",
				"Scope":  "local",
				"IPAM": map[string]any{
					"Config": []map[string]string{{
						"Subnet":  "172.31.0.0/16",
						"Gateway": gateway,
					}},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			writeDockerJSON(t, w, []any{})
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

	pool, err := NewPool(t.Context(), Config{
		DockerClient:       dockerClient,
		RequireHostGateway: true,
	}, discardLogger())
	if err != nil {
		t.Fatalf("NewPool = %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	access, ok := any(pool).(interface {
		NetworkName() string
		HostGateway() string
	})
	if !ok {
		t.Fatal("Pool does not expose its resolved worker network and host gateway")
	}
	if got := access.NetworkName(); got != "bridge" {
		t.Errorf("NetworkName() = %q, want bridge", got)
	}
	if got := access.HostGateway(); got != gateway {
		t.Errorf("HostGateway() = %q, want %q", got, gateway)
	}
}

func TestNewPoolRejectsUnreachableEmbeddedNetwork(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		network    map[string]any
	}{
		{
			name:       "inspect failure",
			statusCode: http.StatusInternalServerError,
		},
		{
			name: "non bridge driver",
			network: map[string]any{
				"Name": "workers", "Driver": "overlay", "Scope": "local",
				"IPAM": map[string]any{"Config": []map[string]string{{"Gateway": "172.31.0.1"}}},
			},
		},
		{
			name: "non local scope",
			network: map[string]any{
				"Name": "workers", "Driver": "bridge", "Scope": "swarm",
				"IPAM": map[string]any{"Config": []map[string]string{{"Gateway": "172.31.0.1"}}},
			},
		},
		{
			name: "no IPv4 gateway",
			network: map[string]any{
				"Name": "workers", "Driver": "bridge", "Scope": "local",
				"IPAM": map[string]any{"Config": []map[string]string{{"Gateway": "fd00::1"}}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/networks/workers"):
					if tt.statusCode != 0 {
						http.Error(w, `{"message":"network inspect failed"}`, tt.statusCode)
						return
					}
					writeDockerJSON(t, w, tt.network)
				case strings.HasSuffix(r.URL.Path, "/containers/json"):
					writeDockerJSON(t, w, []any{})
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

			pool, err := NewPool(t.Context(), Config{
				DockerClient:       dockerClient,
				Network:            "workers",
				RequireHostGateway: true,
			}, discardLogger())
			if err == nil {
				_ = pool.Close()
				t.Fatal("NewPool = nil error, want embedded reachability validation failure")
			}
		})
	}
}

func writeDockerJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode Docker response: %v", err)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}
