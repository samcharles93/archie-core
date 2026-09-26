//go:build unix

package agentexec

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// setProcessGroup puts the harness in its own process group and makes
// cancellation kill the whole group.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

// runAsHarnessUser drops the invocation to the Kit's user. A worker running
// as root refuses to start a harness with no user, because the harness would
// then share the worker's user and could read its credentials.
func runAsHarnessUser(cmd *exec.Cmd, name string) error {
	if name == "" {
		if os.Geteuid() == 0 {
			return errors.New("harness has no user and the worker runs as root; refusing to give the harness the worker's privileges")
		}
		return nil
	}
	cred, err := harnessCredential(name)
	if err != nil {
		return err
	}
	cmd.SysProcAttr.Credential = cred
	return nil
}

// harnessCredential resolves a user name or "uid[:gid]" to a credential.
// Root is refused whichever way it is named.
func harnessCredential(name string) (*syscall.Credential, error) {
	uidText, gidText, hasGid := strings.Cut(name, ":")
	uid, uidErr := strconv.ParseUint(uidText, 10, 32)
	var cred syscall.Credential
	if uidErr == nil {
		cred.Uid, cred.Gid = uint32(uid), uint32(uid)
	} else {
		u, err := user.Lookup(uidText)
		if err != nil {
			return nil, fmt.Errorf("harness user %q: %w", uidText, err)
		}
		id, err := strconv.ParseUint(u.Uid, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("harness user %q has non-numeric uid %q", uidText, u.Uid)
		}
		group, err := strconv.ParseUint(u.Gid, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("harness user %q has non-numeric gid %q", uidText, u.Gid)
		}
		cred.Uid, cred.Gid = uint32(id), uint32(group)
	}
	if hasGid {
		gid, err := strconv.ParseUint(gidText, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("harness group %q is not numeric", gidText)
		}
		cred.Gid = uint32(gid)
	}
	if cred.Uid == 0 {
		return nil, fmt.Errorf("harness user %q is root; a harness never runs with the worker's privileges", name)
	}
	return &cred, nil
}

// ownForHarness gives paths to the harness user when the worker is root, so
// the harness's MCP server can write its captures.
func ownForHarness(name string, paths ...string) error {
	if name == "" || os.Geteuid() != 0 {
		return nil
	}
	cred, err := harnessCredential(name)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if err := os.Chown(p, int(cred.Uid), int(cred.Gid)); err != nil {
			return err
		}
	}
	return nil
}

// ownTreeForHarness gives the worktree to the harness user when the worker
// is root, except .git: the harness edits files and never writes the
// repository, so its refs and config stay the worker's.
func ownTreeForHarness(name, root string) error {
	if name == "" || os.Geteuid() != 0 {
		return nil
	}
	cred, err := harnessCredential(name)
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" && path != root {
			return filepath.SkipDir
		}
		return os.Lchown(path, int(cred.Uid), int(cred.Gid))
	})
}
