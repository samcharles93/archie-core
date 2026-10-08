package container

import (
	"bufio"
	"context"
	"net"

	"github.com/moby/moby/client"

	"github.com/samcharles93/archie-core/internal/infrastructure/kit"
)

// PTY is an interactive exec attached to a container's TTY: writes are
// terminal input, reads are terminal output, and Close closes the hijacked
// socket so a blocked Read unblocks. Docker cannot reattach to a disconnected
// exec, so the caller holds this for the life of the session.
type PTY struct {
	execID string
	conn   net.Conn
	reader *bufio.Reader
	cli    *client.Client
}

// ExecPTY starts argv in a container as user with exactly env, on a TTY. It
// mirrors ExecIn's explicit user and env: the exec must never inherit the
// container's worker environment.
func ExecPTY(ctx context.Context, cli *client.Client, id, user string, env, argv []string, rows, cols uint) (*PTY, error) {
	exec, err := cli.ExecCreate(ctx, id, client.ExecCreateOptions{
		User: user, Env: env, Cmd: argv, WorkingDir: kit.WorkspaceDir,
		TTY: true, AttachStdin: true, AttachStdout: true, AttachStderr: true,
		ConsoleSize: client.ConsoleSize{Height: rows, Width: cols},
	})
	if err != nil {
		return nil, err
	}
	attached, err := cli.ExecAttach(ctx, exec.ID, client.ExecAttachOptions{TTY: true})
	if err != nil {
		return nil, err
	}
	return &PTY{execID: exec.ID, conn: attached.Conn, reader: attached.Reader, cli: cli}, nil
}

// Read reads terminal output. A TTY stream is raw, so there is no
// stdout/stderr to demultiplex.
func (p *PTY) Read(b []byte) (int, error) { return p.reader.Read(b) }

// Write writes terminal input.
func (p *PTY) Write(b []byte) (int, error) { return p.conn.Write(b) }

// ExitCode reports the exec's exit status. It is meaningful only once the
// output stream has ended.
func (p *PTY) ExitCode(ctx context.Context) (int, error) {
	inspected, err := p.cli.ExecInspect(ctx, p.execID, client.ExecInspectOptions{})
	if err != nil {
		return 0, err
	}
	return inspected.ExitCode, nil
}

// Close closes the hijacked socket, which unblocks a blocked Read.
func (p *PTY) Close() error { return p.conn.Close() }
