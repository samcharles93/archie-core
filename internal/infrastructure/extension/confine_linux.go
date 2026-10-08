package extension

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

// launcherArg makes any binary importing this package the confined
// launcher: the extension's parent inside its network namespace.
const launcherArg = "__extension-egress"

// proxyAddr is where the launcher serves the extension's only way out.
const proxyAddr = "127.0.0.1:3128"

func init() {
	if len(os.Args) > 2 && os.Args[1] == launcherArg {
		os.Exit(launch(os.Args[2]))
	}
}

// confinedCommand re-execs this binary as the launcher in a fresh user and
// network namespace. The namespace holds only loopback, so the extension
// cannot reach anything except through the launcher's relay to socket. Where
// the kernel refuses the namespaces, the start fails: there is no unconfined
// fallback.
func confinedCommand(ctx context.Context, binary *os.File, socket string) (*exec.Cmd, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	uid, gid := os.Getuid(), os.Getgid()
	cmd := exec.CommandContext(ctx, self, launcherArg, socket)
	cmd.ExtraFiles = []*os.File{binary}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:  syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET,
		UidMappings: []syscall.SysProcIDMap{{ContainerID: uid, HostID: uid, Size: 1}},
		GidMappings: []syscall.SysProcIDMap{{ContainerID: gid, HostID: gid, Size: 1}},
		// Only the launcher holds this, to bring loopback up; it clears it
		// before the extension starts.
		AmbientCaps: []uintptr{unix.CAP_NET_ADMIN},
		Pdeathsig:   syscall.SIGKILL,
	}
	return cmd, nil
}

// openCommand runs the verified binary by its open descriptor with the host's
// network, for a package that declares no egress hosts.
func openCommand(ctx context.Context, binary *os.File) *exec.Cmd {
	path := fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), binary.Fd())
	cmd := exec.CommandContext(ctx, path) // the host-verified binary, by descriptor
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	return cmd
}

// launch runs inside the namespace: loopback up, relay listening, then the
// extension from descriptor 3 with every capability gone.
func launch(socket string) int {
	ctx := context.Background()
	if err := loopbackUp(); err != nil {
		fmt.Fprintln(os.Stderr, "extension egress: loopback:", err)
		return 1
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", proxyAddr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "extension egress: relay:", err)
		return 1
	}
	go relay(ctx, ln, socket)
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil {
		fmt.Fprintln(os.Stderr, "extension egress: drop capabilities:", err)
		return 1
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		fmt.Fprintln(os.Stderr, "extension egress: no_new_privs:", err)
		return 1
	}
	syscall.CloseOnExec(3)
	return runExtension(ctx, fmt.Sprintf("/proc/%d/fd/3", os.Getpid()))
}

// runExtension starts the extension as the launcher's child and mirrors its
// exit. Pdeathsig ties the child to this thread, so killing the launcher
// kills the extension.
func runExtension(ctx context.Context, path string) int {
	runtime.LockOSThread()
	proxy := "http://" + proxyAddr
	cmd := exec.CommandContext(ctx, path) // the host-verified binary, by descriptor
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(),
		"HTTP_PROXY="+proxy, "HTTPS_PROXY="+proxy, "ALL_PROXY="+proxy,
		"http_proxy="+proxy, "https_proxy="+proxy, "all_proxy="+proxy, "NO_PROXY=", "no_proxy=")
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "extension egress: start:", err)
		return 1
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for s := range signals {
			_ = cmd.Process.Signal(s)
		}
	}()
	err := cmd.Wait()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return exit.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}

func loopbackUp() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	ifr, err := unix.NewIfreq("lo")
	if err != nil {
		return err
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, ifr); err != nil {
		return err
	}
	ifr.SetUint16(ifr.Uint16() | unix.IFF_UP)
	return unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, ifr)
}

// relay carries each proxy connection to the host's gate socket.
func relay(ctx context.Context, ln net.Listener, socket string) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = conn.Close() }()
			upstream, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
			if err != nil {
				return
			}
			defer func() { _ = upstream.Close() }()
			go func() {
				_, _ = io.Copy(upstream, conn)
				closeWrite(upstream)
			}()
			_, _ = io.Copy(conn, upstream)
		}()
	}
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}
