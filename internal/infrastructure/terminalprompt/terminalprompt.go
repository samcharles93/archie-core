// Package terminalprompt is the real, TTY-backed implementation of the
// prompt surface archied setup needs (see internal/infrastructure/
// configuration/setup.Prompter, which this package satisfies structurally
// -- it does not import that package, so there is no infrastructure ->
// infrastructure edge for what is, structurally, just an interface
// satisfaction).
package terminalprompt

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// Terminal prompts over an interactive TTY. The zero value is not usable;
// call [New].
type Terminal struct {
	in  *os.File
	out io.Writer
	r   *bufio.Reader

	// readSecret reads one line from the given fd without echoing it. It is
	// a seam so tests can exercise ReadSecret's prompt/cancellation
	// behaviour over a pipe -- term.ReadPassword itself only works on a
	// real TTY fd, which New already requires for production use, so this
	// field is never swapped outside a test.
	readSecret func(fd int) ([]byte, error)
}

// New returns a Terminal reading from in and writing prompts to out. in
// must be a real terminal: setup's non-interactive failure requirement
// ("a non-TTY invocation without sufficient input fails with a clear
// message rather than writing a partial config") is enforced here, at
// construction, rather than by falling back to a degraded read mode that
// would silently accept piped or redirected input and produce a config
// nobody typed.
func New(in *os.File, out io.Writer) (*Terminal, error) {
	if !term.IsTerminal(int(in.Fd())) {
		return nil, fmt.Errorf(
			"terminalprompt: %s is not a terminal; archied setup requires an "+
				"interactive session (input cannot be piped, redirected, or "+
				"scripted)", in.Name())
	}
	return &Terminal{
		in:         in,
		out:        out,
		r:          bufio.NewReader(in),
		readSecret: term.ReadPassword,
	}, nil
}

// readLine runs a blocking read in a goroutine and races it against
// ctx.Done(). On cancellation the read is abandoned: the goroutine leaks
// until the abandoned read itself returns (real input or EOF), which is
// accepted because a cancelled setup command is expected to exit shortly
// after via its own signal handling, taking the leaked goroutine with it.
func (t *Terminal) readLine(ctx context.Context) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := t.r.ReadString('\n')
		ch <- result{line: strings.TrimRight(line, "\r\n"), err: err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-ch:
		if res.err != nil && !errors.Is(res.err, io.EOF) {
			return "", res.err
		}
		return res.line, nil
	}
}

// readSecretLine mirrors readLine for a non-echoing read. See the doc
// comment on Terminal.readSecret for why term.ReadPassword's own raw-mode
// restoration is not interruptible: an abandoned read on cancellation can
// leave the terminal in raw mode until it unblocks on its own.
func (t *Terminal) readSecretLine(ctx context.Context) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := t.readSecret(int(t.in.Fd()))
		ch <- result{line: string(b), err: err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return "", res.err
		}
		return res.line, nil
	}
}

// Select presents options and returns the chosen index.
func (t *Terminal) Select(ctx context.Context, prompt string, options []string) (int, error) {
	for {
		fmt.Fprintln(t.out, prompt)
		for i, opt := range options {
			fmt.Fprintf(t.out, "  %d) %s\n", i+1, opt)
		}
		fmt.Fprintf(t.out, "Select [1-%d]: ", len(options))
		line, err := t.readLine(ctx)
		if err != nil {
			return -1, err
		}
		n, convErr := strconv.Atoi(strings.TrimSpace(line))
		if convErr != nil || n < 1 || n > len(options) {
			fmt.Fprintf(t.out, "Please enter a number between 1 and %d.\n", len(options))
			continue
		}
		return n - 1, nil
	}
}

// ReadLine reads one line of visible input, falling back to defaultValue
// when the answer is empty.
func (t *Terminal) ReadLine(ctx context.Context, prompt, defaultValue string) (string, error) {
	fmt.Fprint(t.out, prompt)
	line, err := t.readLine(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(line) == "" {
		return defaultValue, nil
	}
	return line, nil
}

// ReadSecret reads one line of input without echoing it.
func (t *Terminal) ReadSecret(ctx context.Context, prompt string) (string, error) {
	fmt.Fprint(t.out, prompt)
	line, err := t.readSecretLine(ctx)
	fmt.Fprintln(t.out)
	if err != nil {
		return "", err
	}
	return line, nil
}

// Confirm asks a yes/no question, falling back to defaultYes when the
// answer is empty.
func (t *Terminal) Confirm(ctx context.Context, prompt string, defaultYes bool) (bool, error) {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	fmt.Fprintf(t.out, "%s %s: ", prompt, suffix)
	line, err := t.readLine(ctx)
	if err != nil {
		return false, err
	}
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defaultYes, nil
	}
	return line == "y" || line == "yes", nil
}
