package releaseupdate

import (
	"archive/zip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestUpdateTransaction(t *testing.T) {
	for _, scenario := range []string{"healthy", "checksum", "unsafe-path", "missing-digest", "busy", "lock", "unhealthy", "crash"} {
		t.Run(scenario, func(t *testing.T) {
			f := newUpdateFixture(t, scenario)
			var lock *os.File
			if scenario == "lock" {
				var err error
				lock, err = os.OpenFile(filepath.Join(f.state, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
				if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
					t.Fatal(err)
				}
				fixtureWrite(t, filepath.Join(f.state, "holder"), "fixture-owner", 0o600)
			}
			args := []string{}
			if scenario == "busy" {
				args = append(args, "--auto")
			}
			output, err := f.run(t, f.installer, args...)
			refusal := map[string]string{"checksum": "checksum mismatch", "unsafe-path": "unsafe archive path", "missing-digest": "digest-pinned", "busy": "work in flight", "lock": "fixture-owner"}[scenario]
			if refusal != "" {
				if err == nil || !strings.Contains(output, refusal) {
					t.Fatalf("wanted %q refusal; err=%v output=%s", refusal, err, output)
				}
				f.assertVersion(t, "old")
				return
			}
			if err != nil {
				t.Fatalf("installer: %v\n%s", err, output)
			}
			f.assertVersion(t, "old") // Staging cannot mutate a running executable.
			old, err := os.Open(filepath.Join(f.bin, "archied"))
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			watchdog := filepath.Join(f.state, "transaction", "watchdog")
			output, err = f.run(t, watchdog)
			if scenario == "crash" {
				if err == nil {
					t.Fatal("expected injected crash")
				}
				f.env = append(f.env, "CRASH_AFTER=")
				output, err = f.run(t, watchdog, "--boot-recover")
			}
			if err != nil {
				t.Fatalf("watchdog: %v\n%s", err, output)
			}
			want := "old"
			if scenario == "healthy" {
				want = "new"
			}
			f.assertVersion(t, want)
			data := make([]byte, 100)
			n, _ := old.Read(data)
			if !strings.Contains(string(data[:n]), "old") {
				t.Fatal("promotion overwrote the open old inode")
			}
			if _, err := os.Stat(filepath.Join(f.state, "transaction")); !os.IsNotExist(err) {
				t.Fatalf("journal not retired: %v", err)
			}
		})
	}
}

type updateFixture struct {
	bin, state, installer string
	env                   []string
}

func fixtureWrite(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}

func newUpdateFixture(t *testing.T, scenario string) updateFixture {
	t.Helper()
	root := t.TempDir()
	bin, state, fake := filepath.Join(root, "bin"), filepath.Join(root, "state"), filepath.Join(root, "fake")
	for _, dir := range []string{bin, state, fake} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	scripts, err := filepath.Abs("../../scripts")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"archied", "archie-state-store", "archie-gateway", "archie-ui", "archie-messaging"} {
		fixtureWrite(t, filepath.Join(bin, name), "#!/bin/bash\n# old\nexit 0\n", 0o755)
	}
	config := filepath.Join(root, "config.toml")
	fixtureWrite(t, config, "[services.state]\n[services.gateway]\n", 0o600)
	archive := makeUpdateArchive(t, root, scripts, scenario)
	sum := sha256.Sum256(archive)
	if scenario == "checksum" {
		sum[0]++
	}
	fixtureWrite(t, filepath.Join(root, "SHA256SUMS"), fmt.Sprintf("%x  archie-core-archied-v2.0.0-linux-amd64.zip\n", sum), 0o600)
	fixtureWrite(t, filepath.Join(fake, "curl"), `#!/bin/bash
out=""
for ((i=1;i<=$#;i++)); do
 if [ "${!i}" = -o ]; then j=$((i+1)); out="${!j}"; fi
 case "${!i}" in
  */SHA256SUMS) source="$FIXTURE/SHA256SUMS" ;;
  *.zip) source="$FIXTURE/archive.zip" ;;
 esac
done
if [ -n "$out" ]; then cp "$source" "$out"; exit; fi
if [ "$SCENARIO" = unhealthy ]; then exit 22; fi
busy=0
[ "$SCENARIO" = busy ] && busy=1
printf '{"status":"ok","version":"2.0.0","in_flight":%s}\n' "$busy"
`, 0o755)
	for _, name := range []string{"systemctl", "systemd-run", "docker"} {
		fixtureWrite(t, filepath.Join(fake, name), "#!/bin/bash\nexit 0\n", 0o755)
	}
	fixtureWrite(t, filepath.Join(fake, "git"), "#!/bin/bash\necho unexpected-git >&2\nexit 98\n", 0o755)
	fixtureWrite(t, filepath.Join(fake, "mv"), `#!/bin/bash
/bin/mv "$@" || exit
if [ -n "$CRASH_AFTER" ] && [[ "$*" == *".archied.new"* ]]; then kill -KILL "$PPID"; fi
`, 0o755)
	return updateFixture{bin: bin, state: state, installer: filepath.Join(scripts, "archie-update-install"), env: append(os.Environ(),
		"PATH="+fake+":"+os.Getenv("PATH"), "FIXTURE="+root, "SCENARIO="+scenario,
		"ARCHIE_BIN_DIR="+bin, "ARCHIE_CONFIG_PATH="+config, "ARCHIE_UPDATE_STATE_DIR="+state,
		"ARCHIE_UPDATE_DAEMON_PREVIOUS=1.0.0", "ARCHIE_UPDATE_DAEMON_VERSION=2.0.0",
		"ARCHIE_UPDATE_AGENT_PREVIOUS=1.0.0", "ARCHIE_UPDATE_AGENT_VERSION="+map[bool]string{true: "2.0.0"}[scenario == "missing-digest"],
		"ARCHIE_UPDATE_HEALTH_TIMEOUT=1", "ARCHIE_UPDATE_OBSERVE_SECONDS=0", "ARCHIE_UPDATE_POLL_INTERVAL=0.01",
		"ARCHIE_UPDATE_STATE_STORE_TARGET=", "CRASH_AFTER="+map[bool]string{true: "archied"}[scenario == "crash"],
	)}
}

func makeUpdateArchive(t *testing.T, root, scripts, scenario string) []byte {
	t.Helper()
	file, err := os.Create(filepath.Join(root, "archive.zip"))
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(file)
	files := map[string]string{"release.json": `{"required_topology":{"units":["archie-state-store","archie-gateway","archie-ui","archie-messaging","archied"],"config_sections":["services.state","services.gateway"]},"agent_image":"mutable:latest"}`}
	for _, name := range []string{"archied", "archie-state-store", "archie-gateway", "archie-ui", "archie-messaging"} {
		files[name] = "#!/bin/bash\n# new\nif [ \"$1\" = -version ]; then echo '" + name + " 2.0.0'; fi\nexit 0\n"
	}
	for _, name := range []string{"archie-update-install", "archie-update-watchdog"} {
		data, err := os.ReadFile(filepath.Join(scripts, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(data)
	}
	if scenario == "unsafe-path" {
		files["../escape"] = "escape"
	}
	for name, body := range files {
		header := &zip.FileHeader{Name: "archie-core-archied-v2.0.0-linux-amd64/" + name, Method: zip.Deflate}
		header.SetMode(0o755)
		w, err := z.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (f updateFixture) run(t *testing.T, path string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), path, args...)
	cmd.Env = f.env
	data, err := cmd.CombinedOutput()
	return string(data), err
}

func (f updateFixture) assertVersion(t *testing.T, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.bin, "archied"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# "+want) {
		t.Fatalf("wanted %s executable: %s", want, data)
	}
}
