//go:build !windows

package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestShellCancellationKillsGrandchildren is the guarantee /stop depends on.
//
// exec.CommandContext's default cancellation kills only the shell it
// started. A shell's real work is a child of that shell, so without an
// explicit process group the command survives its own cancellation: the
// turn unwinds, the user is told it stopped, and `go test ./...` keeps
// running. This drives the case directly -- a backgrounded grandchild that
// outlives its parent shell -- and asserts it is gone.
func TestShellCancellationKillsGrandchildren(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")

	// Background a long sleep, record its pid, then keep the parent shell
	// alive. Only a group-wide signal reaps the sleep.
	command := "sleep 300 & echo $! > " + pidFile + "; sleep 300"
	params, err := json.Marshal(ShellParams{Command: command, Timeout: 60})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = makeShellExecutor(dir, nil)(ctx, params, NonInteractiveBridge{})
	}()

	pid := waitForPID(t, pidFile)
	if !processAlive(pid) {
		t.Fatalf("grandchild %d was not running before cancellation", pid)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the shell tool did not return after cancellation")
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Do not leave a stray sleep behind if the assertion fails.
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("grandchild %d survived cancellation; /stop would not have stopped it", pid)
}

// waitForPID blocks until the shell has recorded its background child's pid.
func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); convErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the shell never recorded a background child pid")
	return 0
}

// processAlive reports whether pid still exists. Signal 0 performs the
// permission and existence checks without delivering anything.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
