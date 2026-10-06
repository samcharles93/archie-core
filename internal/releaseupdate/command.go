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

// ScriptInstaller runs the install script a release places beside this binary.
type ScriptInstaller struct {
	HealthURL string
}

func (s ScriptInstaller) path() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	path := filepath.Join(filepath.Dir(exe), "archie-update-install")
	if info, err := os.Stat(path); err != nil || info.Mode()&0o111 == 0 {
		return ""
	}
	return path
}

func (s ScriptInstaller) Install(ctx context.Context, snapshot Snapshot, meta InstallMeta, progress func(string)) (Result, error) {
	path := s.path()
	if path == "" {
		return Result{}, ErrNotConfigured
	}
	return CommandInstaller{Command: []string{path}, HealthURL: s.HealthURL}.Install(ctx, snapshot, meta, progress)
}

// Installable reports whether the install script is present.
func (s ScriptInstaller) Installable(context.Context) bool { return s.path() != "" }
