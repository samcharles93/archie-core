// Package extension supervises extension plugins: one go-plugin subprocess per
// enabled extension, each serving a gRPC surface from proto/<surface>/v1.
package extension

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
	// Egress is the accepted egress host list. When it names hosts, the
	// extension runs in its own network namespace and reaches only these,
	// through a proxy; an empty list runs it on the host's network.
	Egress []string
}

// Host owns the running extension processes, keyed by Spec.Name.
type Host struct {
	log hclog.Logger

	mu    sync.Mutex
	procs map[string]*proc
}

// proc is one running extension and the egress gate it alone can reach.
type proc struct {
	client *goplugin.Client
	gate   *gate
}

func (p *proc) kill() {
	p.client.Kill()
	if p.gate != nil {
		p.gate.close()
	}
}

// command confines the extension to egress when the package declares hosts.
// A package that declares none opted out of confinement and gets the host's
// network, with no gate.
func command(ctx context.Context, binary *os.File, egress []string) (*exec.Cmd, *gate, error) {
	if len(egress) == 0 {
		return openCommand(ctx, binary), nil, nil
	}
	g, err := openGate(ctx, egress)
	if err != nil {
		return nil, nil, fmt.Errorf("egress: %w", err)
	}
	cmd, err := confinedCommand(ctx, binary, g.socket)
	if err != nil {
		g.close()
		return nil, nil, err
	}
	return cmd, g, nil
}

// NewHost returns a Host that reports plugin lifecycle problems to log.
func NewHost(log hclog.Logger) *Host {
	return &Host{log: log, procs: make(map[string]*proc)}
}

// Start launches spec serving surface and returns the dispensed client. An
// extension already running under the same name is stopped first, so Start is
// also how a changed binary or setting takes effect.
func (h *Host) Start(ctx context.Context, spec Spec, surface string, impl goplugin.Plugin) (any, error) {
	sum, err := hex.DecodeString(spec.SHA256)
	if err != nil || len(sum) != sha256.Size {
		return nil, fmt.Errorf("extension %q: sha256 must be a hex digest", spec.Name)
	}
	// The verified file is what runs: the confined launcher execs this open
	// descriptor, so swapping the path after the check changes nothing.
	binary, err := openVerified(spec.Path, sum)
	if err != nil {
		return nil, fmt.Errorf("extension %q: %w", spec.Name, err)
	}
	defer func() { _ = binary.Close() }()
	cmd, g, err := command(ctx, binary, spec.Egress)
	if err != nil {
		return nil, fmt.Errorf("extension %q: %w", spec.Name, err)
	}
	cmd.Env = passEnv(spec.Env)
	p := &proc{gate: g, client: goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig:  Handshake,
		Plugins:          goplugin.PluginSet{surface: impl},
		Cmd:              cmd,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		SkipHostEnv:      true,
		Logger:           h.log.Named(spec.Name),
	})}
	rpc, err := p.client.Client()
	if err != nil {
		p.kill()
		return nil, fmt.Errorf("extension %q: connect: %w", spec.Name, err)
	}
	raw, err := rpc.Dispense(surface)
	if err != nil {
		p.kill()
		return nil, fmt.Errorf("extension %q: dispense %s: %w", spec.Name, surface, err)
	}
	h.mu.Lock()
	old := h.procs[spec.Name]
	h.procs[spec.Name] = p
	h.mu.Unlock()
	if old != nil {
		old.kill()
	}
	return raw, nil
}

// Stop kills the named extension. Stopping one that is not running is not an
// error.
func (h *Host) Stop(name string) {
	h.mu.Lock()
	p := h.procs[name]
	delete(h.procs, name)
	h.mu.Unlock()
	if p != nil {
		p.kill()
	}
}

// Alive reports whether the named extension's process is still running.
func (h *Host) Alive(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.procs[name]
	return p != nil && !p.client.Exited()
}

// Close stops every extension.
func (h *Host) Close() {
	h.mu.Lock()
	procs := h.procs
	h.procs = make(map[string]*proc)
	h.mu.Unlock()
	for _, p := range procs {
		p.kill()
	}
}

// openVerified opens the extension binary and checks it against sum.
func openVerified(path string, sum []byte) (*os.File, error) {
	f, err := os.Open(path) // the path is the installed package's binary
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		_ = f.Close()
		return nil, err
	}
	if !bytes.Equal(h.Sum(nil), sum) {
		_ = f.Close()
		return nil, errors.New("binary does not match its checksum")
	}
	return f, nil
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
