// Package extension supervises extension plugins: one go-plugin subprocess per
// enabled extension, each serving a gRPC surface from proto/<surface>/v1.
package extension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
)

// Handshake is shared by the host and every extension binary. Bump
// ProtocolVersion on an incompatible change to the handshake itself; each
// surface versions its own proto package.
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "ARCHIE_EXTENSION",
	MagicCookieValue: "archie",
}

// Spec is one extension to run.
type Spec struct {
	Name string
	// Path is the extension binary.
	Path string
	// SHA256 is the hex digest the binary must match before it is launched.
	SHA256 string
	// Env names the host environment variables the extension may see. PATH and
	// HOME are always passed so a plugin can find the CLI it wraps.
	Env []string
}

// Host owns the running extension processes, keyed by Spec.Name.
type Host struct {
	log hclog.Logger

	mu    sync.Mutex
	procs map[string]*goplugin.Client
}

// NewHost returns a Host that reports plugin lifecycle problems to log.
func NewHost(log hclog.Logger) *Host {
	return &Host{log: log, procs: make(map[string]*goplugin.Client)}
}

// Start launches spec serving surface and returns the dispensed client. An
// extension already running under the same name is stopped first, so Start is
// also how a changed binary or setting takes effect.
func (h *Host) Start(ctx context.Context, spec Spec, surface string, impl goplugin.Plugin) (any, error) {
	sum, err := hex.DecodeString(spec.SHA256)
	if err != nil || len(sum) != sha256.Size {
		return nil, fmt.Errorf("extension %q: sha256 must be a hex digest", spec.Name)
	}
	cmd := exec.CommandContext(ctx, spec.Path)
	cmd.Env = passEnv(spec.Env)
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig:  Handshake,
		Plugins:          goplugin.PluginSet{surface: impl},
		Cmd:              cmd,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		SecureConfig:     &goplugin.SecureConfig{Checksum: sum, Hash: sha256.New()},
		SkipHostEnv:      true,
		Logger:           h.log.Named(spec.Name),
	})
	rpc, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("extension %q: connect: %w", spec.Name, err)
	}
	raw, err := rpc.Dispense(surface)
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("extension %q: dispense %s: %w", spec.Name, surface, err)
	}
	h.mu.Lock()
	old := h.procs[spec.Name]
	h.procs[spec.Name] = client
	h.mu.Unlock()
	if old != nil {
		old.Kill()
	}
	return raw, nil
}

// Stop kills the named extension. Stopping one that is not running is not an
// error.
func (h *Host) Stop(name string) {
	h.mu.Lock()
	client := h.procs[name]
	delete(h.procs, name)
	h.mu.Unlock()
	if client != nil {
		client.Kill()
	}
}

// Alive reports whether the named extension's process is still running.
func (h *Host) Alive(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	client := h.procs[name]
	return client != nil && !client.Exited()
}

// Close stops every extension.
func (h *Host) Close() {
	h.mu.Lock()
	procs := h.procs
	h.procs = make(map[string]*goplugin.Client)
	h.mu.Unlock()
	for _, client := range procs {
		client.Kill()
	}
}

func passEnv(names []string) []string {
	env := make([]string, 0, len(names)+2)
	for _, name := range append([]string{"PATH", "HOME"}, names...) {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}
