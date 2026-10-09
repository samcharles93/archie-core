// Package harnesssession runs the daemon side of a harness session: it builds
// an ephemeral Kit-profile container, opens a PTY into it as the Kit's harness
// user, and tears the container down when the session closes. It owns no
// transport; internal/app/archied serves the gRPC contract over it.
package harnesssession

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/moby/moby/client"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/container"
	"github.com/samcharles93/archie-core/internal/infrastructure/kitrun"
)

// The address errors a caller distinguishes: an unknown profile is the
// caller's mistake, not a daemon that cannot run containers.
var (
	ErrProfileUnknown = errors.New("agent profile is not configured")
	ErrProfileNotKit  = errors.New("agent profile is not a Kit profile")
	// ErrNoShell reports a Kit image that provides no login shell, so a setup
	// terminal cannot be opened in it.
	ErrNoShell = errors.New("the Kit image provides no shell")
)

// Launcher starts and ends a setup session's container. *kitrun.Launcher
// satisfies it.
type Launcher interface {
	LaunchSetup(ctx context.Context, req kitrun.SetupRequest) (*kitrun.Run, error)
	Release(ctx context.Context, run *kitrun.Run) error
}

// Manager opens setup sessions.
type Manager struct {
	Launcher Launcher
	// Client is the container pool's Docker client, used for the PTY exec.
	Client *client.Client
	// Profile resolves an agent profile by name from the running config.
	Profile func(name string) (config.AgentProfile, error)
	// TTL bounds a session's life. Zero means the container pool's max-uptime
	// reaper is the only bound.
	TTL time.Duration
}

// Terminal geometry: the fallback when the client asks for no size, and the
// largest the PTY is created with.
const (
	terminalDefaultRows = 24
	terminalDefaultCols = 80
	terminalMaxCells    = 1000
)

// Open starts a setup session at the requested terminal size, in character
// cells, and returns its duplex PTY. A zero or out-of-range size falls back to
// the default rather than failing the open.
func (m *Manager) Open(ctx context.Context, org, profile string, rows, cols int) (*Session, error) {
	p, err := m.Profile(profile)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrProfileUnknown, profile)
	}
	if !p.IsKit() {
		return nil, fmt.Errorf("%w: %s", ErrProfileNotKit, profile)
	}
	id, err := sessionID()
	if err != nil {
		return nil, err
	}
	run, err := m.Launcher.LaunchSetup(ctx, kitrun.SetupRequest{Session: id, Kit: p.Kit, Org: org})
	if err != nil {
		return nil, err
	}
	shell, err := pickShell(func(argv []string) (int, error) {
		code, _, err := container.ExecIn(ctx, m.Client, run.Container.ID, run.Harness.User, run.Harness.Env, argv)
		return code, err
	})
	if err != nil {
		_ = m.Launcher.Release(context.WithoutCancel(ctx), run)
		return nil, fmt.Errorf("%w: %s", ErrNoShell, p.Kit)
	}
	pty, err := container.ExecPTY(ctx, m.Client, run.Container.ID, run.Harness.User, run.Harness.Env, shell,
		uint(terminalSize(rows, terminalDefaultRows)), uint(terminalSize(cols, terminalDefaultCols)))
	if err != nil {
		_ = m.Launcher.Release(context.WithoutCancel(ctx), run)
		return nil, err
	}
	s := &Session{pty: pty, run: run, release: m.Launcher.Release}
	if m.TTL > 0 {
		//nolint:contextcheck // teardown must outlive the stream context that opened the session
		s.timer = time.AfterFunc(m.TTL, func() { _ = s.Close() })
	}
	return s, nil
}

// Session is one open setup session. Close is idempotent.
type Session struct {
	pty     *container.PTY
	run     *kitrun.Run
	release func(context.Context, *kitrun.Run) error
	timer   *time.Timer

	once sync.Once
}

func (s *Session) Read(p []byte) (int, error)  { return s.pty.Read(p) }
func (s *Session) Write(p []byte) (int, error) { return s.pty.Write(p) }

// ExitCode reports the shell's exit status once its output has ended.
func (s *Session) ExitCode(ctx context.Context) (int, error) { return s.pty.ExitCode(ctx) }

// Close ends the PTY and tears the session down: the container, its volumes,
// its network and its egress session. Teardown uses its own context because
// the session's is usually already cancelled.
func (s *Session) Close() error {
	var err error
	s.once.Do(func() {
		if s.timer != nil {
			s.timer.Stop()
		}
		_ = s.pty.Close()
		err = s.release(context.Background(), s.run)
	})
	return err
}

// terminalSize bounds a requested row or column count.
func terminalSize(v, def int) int {
	if v < 1 || v > terminalMaxCells {
		return def
	}
	return v
}

// pickShell returns the login shell the image provides, preferring bash over
// sh, or ErrNoShell when it provides neither. The image, not archie, decides
// what is installed, so the probe runs in the container.
func pickShell(probe func(argv []string) (int, error)) ([]string, error) {
	for _, shell := range []string{"bash", "sh"} {
		if code, err := probe([]string{shell, "-c", "exit 0"}); err == nil && code == 0 {
			return []string{shell, "-l"}, nil
		}
	}
	return nil, ErrNoShell
}

// sessionID is the per-session key for the container, network and volumes. It
// is random so two sessions for one profile never collide on a container name.
func sessionID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mint session id: %w", err)
	}
	return "setup-" + hex.EncodeToString(b[:]), nil
}
