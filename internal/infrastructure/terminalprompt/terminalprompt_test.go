package terminalprompt

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// New must refuse a non-terminal input with a clear message rather than
// silently degrading to a plain read -- that degraded path is exactly what
// would let a piped/scripted invocation produce a partial config nobody
// typed.
func TestNew_RejectsNonTerminalInput(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	_, err = New(r, io.Discard)
	if err == nil {
		t.Fatal("New(pipe) = nil error, want a rejection")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("New(pipe) error = %q, want it to explain the input is not a terminal", err.Error())
	}
}

// newForTest builds a Terminal directly, bypassing New's TTY gate, so the
// prompt/parsing logic can be exercised over a plain pipe. Production code
// only ever constructs a Terminal through New; this mirrors what the
// package review called for -- inject the fd, don't weaken the real
// constructor for testability.
func newForTest(t *testing.T, in io.Reader, readSecret func(int) ([]byte, error)) (*Terminal, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	return &Terminal{
		in:         nil,
		out:        &out,
		r:          bufio.NewReader(in),
		readSecret: readSecret,
	}, &out
}

func TestSelect_ParsesAValidChoice(t *testing.T) {
	term, out := newForTest(t, strings.NewReader("2\n"), nil)
	i, err := term.Select(context.Background(), "Pick one", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if i != 1 {
		t.Errorf("Select() = %d, want 1", i)
	}
	if !strings.Contains(out.String(), "Pick one") {
		t.Errorf("prompt not written to out: %q", out.String())
	}
}

func TestSelect_RepromptsOnInvalidChoiceThenAccepts(t *testing.T) {
	term, out := newForTest(t, strings.NewReader("nope\n99\n1\n"), nil)
	i, err := term.Select(context.Background(), "Pick one", []string{"a", "b"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if i != 0 {
		t.Errorf("Select() = %d, want 0", i)
	}
	if strings.Count(out.String(), "Pick one") != 3 {
		t.Errorf("expected the prompt to repeat for each invalid answer, got:\n%s", out.String())
	}
}

func TestReadLine_EmptyAnswerReturnsDefault(t *testing.T) {
	term, _ := newForTest(t, strings.NewReader("\n"), nil)
	got, err := term.ReadLine(context.Background(), "Name: ", "fallback")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if got != "fallback" {
		t.Errorf("ReadLine() = %q, want %q", got, "fallback")
	}
}

func TestReadLine_NonEmptyAnswerWins(t *testing.T) {
	term, _ := newForTest(t, strings.NewReader("Ada\n"), nil)
	got, err := term.ReadLine(context.Background(), "Name: ", "fallback")
	if err != nil {
		t.Fatalf("ReadLine: %v", err)
	}
	if got != "Ada" {
		t.Errorf("ReadLine() = %q, want %q", got, "Ada")
	}
}

func TestConfirm(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		defaultYes bool
		want       bool
	}{
		{name: "empty answer takes the default (yes)", input: "\n", defaultYes: true, want: true},
		{name: "empty answer takes the default (no)", input: "\n", defaultYes: false, want: false},
		{name: "y overrides a false default", input: "y\n", defaultYes: false, want: true},
		{name: "yes overrides a false default", input: "yes\n", defaultYes: false, want: true},
		{name: "n overrides a true default", input: "n\n", defaultYes: true, want: false},
		{name: "case insensitive", input: "Y\n", defaultYes: false, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			term, _ := newForTest(t, strings.NewReader(tc.input), nil)
			got, err := term.Confirm(context.Background(), "Continue?", tc.defaultYes)
			if err != nil {
				t.Fatalf("Confirm: %v", err)
			}
			if got != tc.want {
				t.Errorf("Confirm() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReadSecret_UsesTheInjectedReaderNotEcho(t *testing.T) {
	fakeReadSecret := func(fd int) ([]byte, error) {
		return []byte("s3cr3t"), nil
	}
	term, out := newForTest(t, strings.NewReader(""), fakeReadSecret)
	got, err := term.ReadSecret(context.Background(), "Token: ")
	if err != nil {
		t.Fatalf("ReadSecret: %v", err)
	}
	if got != "s3cr3t" {
		t.Errorf("ReadSecret() = %q, want %q", got, "s3cr3t")
	}
	if strings.Contains(out.String(), "s3cr3t") {
		t.Errorf("secret value must never be written to out, got: %q", out.String())
	}
}

func TestReadSecret_PropagatesReadError(t *testing.T) {
	wantErr := errors.New("boom")
	fakeReadSecret := func(fd int) ([]byte, error) { return nil, wantErr }
	term, _ := newForTest(t, strings.NewReader(""), fakeReadSecret)
	_, err := term.ReadSecret(context.Background(), "Token: ")
	if !errors.Is(err, wantErr) {
		t.Fatalf("ReadSecret() error = %v, want %v", err, wantErr)
	}
}

// A cancelled context must return promptly rather than block forever on a
// read that will never come -- e.g. Ctrl+C at a prompt with no answer
// typed yet.
func TestReadLine_CancelledContextReturnsPromptly(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close() // never written to: the read blocks until cancellation

	term, _ := newForTest(t, r, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		_, err := term.ReadLine(ctx, "Name: ", "")
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("ReadLine() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadLine did not return promptly after cancellation")
	}
}

func TestReadSecret_CancelledContextReturnsPromptly(t *testing.T) {
	blockForever := func(fd int) ([]byte, error) {
		select {} // never returns; simulates a real blocking terminal read
	}
	term, _ := newForTest(t, strings.NewReader(""), blockForever)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		_, err := term.ReadSecret(ctx, "Token: ")
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("ReadSecret() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadSecret did not return promptly after cancellation")
	}
}
