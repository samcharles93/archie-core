package archied

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestUpdateTopologyRefusal(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "release.json")
	config := filepath.Join(root, "config.toml")
	writeUpdateFixture(t, manifest, `{"required_topology":{"units":["archie-state-store","archie-gateway","archied"],"config_sections":["services.state","services.gateway"]}}`)
	writeUpdateFixture(t, config, "[services.state]\ntarget='localhost:9090'\n")
	writeUpdateFixture(t, filepath.Join(root, "systemctl"), "#!/bin/sh\nprintf 'archied.service enabled enabled\\n'\n")
	t.Setenv("PATH", root+":"+os.Getenv("PATH"))
	var output bytes.Buffer
	if err := checkUpdateTopology(config, manifest, &output); err == nil {
		t.Fatal("partial topology accepted")
	}
	for _, missing := range []string{"configure [services.gateway]", "install and enable archie-state-store.service", "install and enable archie-gateway.service"} {
		if !strings.Contains(output.String(), missing) {
			t.Fatalf("missing step %q: %s", missing, output.String())
		}
	}
}

func TestResumeUpdate(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "interrupted", true: "live"}[active], func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("ARCHIE_UPDATE_STATE_DIR", root)
			if err := os.Mkdir(filepath.Join(root, "transaction"), 0o700); err != nil {
				t.Fatal(err)
			}
			called := filepath.Join(root, "called")
			writeUpdateFixture(t, filepath.Join(root, "systemd-run"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+called+"'\n")
			t.Setenv("PATH", root+":"+os.Getenv("PATH"))
			lock, err := os.OpenFile(filepath.Join(root, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if active {
				if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
					t.Fatal(err)
				}
			}
			resumed, err := resumeUpdate(t.Context())
			if err != nil || resumed == active {
				t.Fatalf("resumed=%v err=%v", resumed, err)
			}
			if !active {
				data, err := os.ReadFile(called)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(data), "--boot-recover") {
					t.Fatalf("not a rollback: %s", data)
				}
			}
		})
	}
}

func writeUpdateFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o700); err != nil {
		t.Fatal(err)
	}
}
