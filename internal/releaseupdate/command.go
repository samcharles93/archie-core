package releaseupdate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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
type CommandCatalog struct{ Command []string }

func (c CommandCatalog) Check(ctx context.Context) (Snapshot, error) {
	if len(c.Command) == 0 {
		return Snapshot{}, fmt.Errorf("update check command is empty")
	}
	output, err := exec.CommandContext(ctx, c.Command[0], c.Command[1:]...).Output()
	if err != nil {
		return Snapshot{}, fmt.Errorf("run update check: %w", err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(output, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode update check output: %w", err)
	}
	return snapshot, nil
}

type CommandInstaller struct{ Command []string }

// Install runs the configured command and streams its stdout to progress
// line by line as the command emits it -- the earlier CombinedOutput-based
// version buffered everything until exit, so callers saw nothing until the
// whole update either finished or failed. One stdout line may be a
// structured Result instead of prose; see updateResultSentinel.
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
