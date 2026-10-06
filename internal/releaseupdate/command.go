package releaseupdate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// updateResultSentinel prefixes the one line an install command may print to
// report its structured Result. Every other line is forwarded to progress
// as-is.
const updateResultSentinel = "ARCHIE_UPDATE_RESULT "

// CommandCatalog reads a Snapshot JSON document from an explicitly configured
// argv command. It never invokes a shell; deployment tooling remains an
// administrator-owned adapter.
type CommandCatalog struct {
	Command []string
	Env     []string
}

func (c CommandCatalog) Check(ctx context.Context) (Snapshot, error) {
	if len(c.Command) == 0 {
		return Snapshot{}, fmt.Errorf("update check command is empty")
	}
	cmd := exec.CommandContext(ctx, c.Command[0], c.Command[1:]...)
	cmd.Env = append(cmd.Environ(), c.Env...)
	output, err := cmd.Output()
	if err != nil {
		return Snapshot{}, fmt.Errorf("run update check: %w", err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(output, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode update check output: %w", err)
	}
	return snapshot, nil
}

// CommandInstaller runs the deployment's install command. HealthURL is
// passed as ARCHIE_HEALTH_URL when set.
type CommandInstaller struct {
	Command   []string
	HealthURL string
}

// Install runs the command, streaming stdout lines to progress. One line may
// be a structured Result.
func (i CommandInstaller) Install(ctx context.Context, snapshot Snapshot, meta InstallMeta, progress func(string)) (Result, error) {
	if len(i.Command) == 0 {
		return Result{}, fmt.Errorf("update install command is empty")
	}
	cmd := exec.CommandContext(ctx, i.Command[0], i.Command[1:]...)
	versions := componentVersions(snapshot)
	cmd.Env = append(
		cmd.Environ(),
		"ARCHIE_UPDATE_CHANNEL="+meta.Channel,
		"ARCHIE_UPDATE_CHAT_ID="+strconv.FormatInt(meta.ChatID, 10),
		"ARCHIE_UPDATE_THREAD_ID="+strconv.Itoa(meta.ThreadID),
		"ARCHIE_UPDATE_REPORT_PATH="+meta.ReportPath,
		"ARCHIE_UPDATE_DAEMON_PREVIOUS="+versions[ComponentDaemon].Installed,
		"ARCHIE_UPDATE_DAEMON_VERSION="+versions[ComponentDaemon].Available,
		"ARCHIE_UPDATE_AGENT_PREVIOUS="+versions[ComponentAgent].Installed,
		"ARCHIE_UPDATE_AGENT_VERSION="+versions[ComponentAgent].Available,
	)
	if i.HealthURL != "" {
		cmd.Env = append(cmd.Env, "ARCHIE_HEALTH_URL="+i.HealthURL)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("open update installer stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("start update installer: %w", err)
	}

	var result Result
	var resultErr error
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if rest, ok := strings.CutPrefix(line, updateResultSentinel); ok {
			if err := json.Unmarshal([]byte(rest), &result); err != nil {
				resultErr = fmt.Errorf("decode update installer result: %w", err)
			}
			continue
		}
		progress(line)
	}
	if err := scanner.Err(); err != nil {
		resultErr = fmt.Errorf("read update installer output: %w", err)
	}

	waitErr := cmd.Wait()
	if waitErr != nil {
		return result, fmt.Errorf("run update installer: %w: %s", waitErr, stderr.String())
	}
	if resultErr != nil {
		return result, resultErr
	}
	return result, nil
}

// componentVersions reduces an approved snapshot to the two components the
// reference binary installer owns. Available is intentionally blank for an
// unchanged component: the install script treats these values as the complete
// approved plan, not as hints to rediscover work after approval.
func componentVersions(snapshot Snapshot) map[string]Component {
	versions := map[string]Component{
		ComponentDaemon: {},
		ComponentAgent:  {},
	}
	for _, component := range snapshot.Components {
		if component.ID != ComponentDaemon && component.ID != ComponentAgent {
			continue
		}
		if component.Available == component.Installed {
			component.Available = ""
		}
		versions[component.ID] = component
	}
	return versions
}

// ErrNotConfigured is returned when the step's command is unset.
var ErrNotConfigured = errors.New("updates are not configured")

// Commands is what a deployment runs to check for and install updates. Env
// is added to the check's environment.
type Commands struct {
	Check   []string
	Install []string
	Env     []string
}

// ScriptCommands runs the update scripts a release installs beside this
// binary, selecting releases from channel. Only these scripts ever run, so a
// setting cannot name an arbitrary program. A missing script is unset.
func ScriptCommands(channel, pin string) Commands {
	exe, err := os.Executable()
	if err != nil {
		return Commands{}
	}
	script := func(name string) []string {
		path := filepath.Join(filepath.Dir(exe), name)
		if info, err := os.Stat(path); err != nil || info.Mode()&0o111 == 0 {
			return nil
		}
		return []string{path}
	}
	return Commands{
		Check:   script("archie-update-check"),
		Install: script("archie-update-install"),
		Env:     []string{"ARCHIE_UPDATE_RELEASE_CHANNEL=" + channel, "ARCHIE_UPDATE_PIN=" + pin},
	}
}

// SettingsCommands is both Catalog and Installer. It loads Commands on every
// call so an edited setting applies without a restart.
type SettingsCommands struct {
	Load      func(context.Context) (Commands, error)
	HealthURL string
}

func (s SettingsCommands) Check(ctx context.Context) (Snapshot, error) {
	commands, err := s.Load(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if len(commands.Check) == 0 {
		return Snapshot{}, ErrNotConfigured
	}
	return CommandCatalog{Command: commands.Check, Env: commands.Env}.Check(ctx)
}

func (s SettingsCommands) Install(ctx context.Context, snapshot Snapshot, meta InstallMeta, progress func(string)) (Result, error) {
	commands, err := s.Load(ctx)
	if err != nil {
		return Result{}, err
	}
	if len(commands.Install) == 0 {
		return Result{}, ErrNotConfigured
	}
	return CommandInstaller{Command: commands.Install, HealthURL: s.HealthURL}.Install(ctx, snapshot, meta, progress)
}

// Installable reports whether an install command is set.
func (s SettingsCommands) Installable(ctx context.Context) bool {
	commands, err := s.Load(ctx)
	return err == nil && len(commands.Install) != 0
}
