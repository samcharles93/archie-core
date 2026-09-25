package agentexec

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
)

// Harness outcomes beyond StatusPassed. The strings match the built-in
// loop's, so a stage reads the same whichever runner ran it.
const (
	StatusParked = "parked"
	StopTimedOut = "timed_out"
	StopPolicy   = "policy_violation"
	StopGate     = "gate_failed"
	StopExited   = "harness_exited"
)

const (
	promptPlaceholder  = "{{.Prompt}}"
	sessionPlaceholder = "{{.SessionID}}"
	stderrTailBytes    = 2000
	killGrace          = 2 * time.Second
)

// HarnessOutput reads one harness invocation's standard output. An adapter
// knows its CLI's stream format; it reports tool calls as they complete and
// the session to resume.
type HarnessOutput interface {
	Line(line []byte, report ToolCallReporter)
	SessionID() string
	Usage() Usage
}

// HarnessRunner runs a stage on an external coding-agent CLI. Archie's
// guarantees do not depend on the CLI obeying its prompt: the runner checks
// the worktree and the repository's refs and configuration after every
// invocation, runs the gate itself, and owns the process's lifetime.
type HarnessRunner struct {
	newOutput func() HarnessOutput
}

// NewHarnessRunner returns a runner using newOutput to read each
// invocation. A nil newOutput reads nothing: no tool calls, usage or session.
func NewHarnessRunner(newOutput func() HarnessOutput) *HarnessRunner {
	return &HarnessRunner{newOutput: newOutput}
}

func (r *HarnessRunner) Run(ctx context.Context, workspace string, req Request, report ToolCallReporter) (Result, error) {
	if err := validateHarness(req); err != nil {
		return Result{}, err
	}
	res := Result{Version: ProtocolVersion, TaskID: req.TaskID, Attempt: req.Attempt, Stage: req.Stage}
	before, err := snapshotRepo(workspace)
	if err != nil {
		return Result{}, fmt.Errorf("snapshot workspace before harness: %w", err)
	}
	runCtx := ctx
	if req.Budget.WallClock > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, req.Budget.WallClock)
		defer cancel()
	}

	maxIterations := 1
	if len(req.Harness.Resume) > 0 || len(req.Harness.Continue) > 0 {
		maxIterations = max(1, req.Gate.MaxConsecutiveFailures)
	}
	prompt := harnessPrompt(workspace, req)
	verb := req.Harness.Prompt
	session := ""
	for {
		res.Iterations++
		out := r.output()
		exitErr := invoke(runCtx, workspace, req.Harness, harnessArgv(req.Harness, verb, prompt, session), out, report)
		if id := out.SessionID(); id != "" {
			session = id
		}
		res.Usage = addUsage(res.Usage, out.Usage())
		res.TokensUsed = res.Usage.TotalTokens
		if ctx.Err() != nil {
			return res, ctx.Err()
		}

		res, stopped, err := settle(runCtx, workspace, before, req, res, exitErr)
		if err != nil || stopped {
			return res, err
		}

		gateOutput, gateErr := runHarnessGate(runCtx, req.Gate.Commands, workspace)
		if gateErr == nil {
			res.Status = StatusPassed
			return res, nil
		}
		if runCtx.Err() != nil {
			return park(res, StopTimedOut, "harness exceeded its wall-clock budget during the gate"), nil
		}
		if res.Iterations >= maxIterations {
			return park(res, StopGate, gateOutput), nil
		}
		verb, prompt = resumeVerb(req.Harness, session), "The quality gate failed. Fix the cause, then stop.\n\n"+gateOutput
	}
}

// settle checks one finished invocation. Guarantees come first: a policy
// violation parks the step even when the invocation also timed out or
// failed, because what the harness did matters more than how it ended.
func settle(runCtx context.Context, workspace string, before repoState, req Request, res Result, exitErr error) (Result, bool, error) {
	after, err := snapshotRepo(workspace)
	if err != nil {
		return res, true, fmt.Errorf("snapshot workspace after harness: %w", err)
	}
	res.Changes = changedPaths(before, after)
	// An exhausted budget is an outcome of the step, not a runner error.
	outOfTime := errors.Is(runCtx.Err(), context.DeadlineExceeded)
	switch violation := policyViolation(before, after, res.Changes, req); {
	case violation != "":
		return park(res, StopPolicy, violation), true, nil
	case outOfTime:
		return park(res, StopTimedOut, "harness exceeded its wall-clock budget"), true, nil
	case exitErr != nil:
		return park(res, StopExited, exitErr.Error()), true, nil
	}
	return res, false, nil
}

func (r *HarnessRunner) output() HarnessOutput {
	if r.newOutput == nil {
		return silentOutput{}
	}
	return r.newOutput()
}

func validateHarness(req Request) error {
	if err := req.Validate(); err != nil {
		return err
	}
	h := req.Harness
	if h == nil || len(h.Launch) == 0 {
		return errors.New("harness runner needs a harness spec with a launch command")
	}
	if !slices.ContainsFunc(h.Prompt, func(s string) bool { return strings.Contains(s, promptPlaceholder) }) {
		return fmt.Errorf("harness prompt verb must carry %s, or the mission is never passed", promptPlaceholder)
	}
	if len(h.Resume) > 0 && !slices.ContainsFunc(h.Resume, func(s string) bool { return strings.Contains(s, sessionPlaceholder) }) {
		return fmt.Errorf("harness resume verb must carry %s", sessionPlaceholder)
	}
	return nil
}

// harnessArgv is the launch command, then the verb, then the prompt verb
// when the verb is a resume or continue, with placeholders substituted as
// raw values rather than shell text.
func harnessArgv(h *HarnessSpec, verb []string, prompt, session string) []string {
	argv := slices.Clone(h.Launch)
	tails := [][]string{verb}
	if !slices.Equal(verb, h.Prompt) {
		tails = append(tails, h.Prompt)
	}
	for _, tail := range tails {
		for _, arg := range tail {
			arg = strings.ReplaceAll(arg, promptPlaceholder, prompt)
			arg = strings.ReplaceAll(arg, sessionPlaceholder, session)
			argv = append(argv, arg)
		}
	}
	return argv
}

// resumeVerb continues the session the last invocation reported, falling
// back to the most recent session when the CLI reported none.
func resumeVerb(h *HarnessSpec, sessionID string) []string {
	if len(h.Resume) > 0 && sessionID != "" {
		return slices.Clone(h.Resume)
	}
	if len(h.Continue) > 0 {
		return slices.Clone(h.Continue)
	}
	return slices.Clone(h.Resume)
}

// invoke runs one harness invocation in its own process group, feeding
// stdout to out line by line. Cancelling ctx kills the whole group, so a
// CLI's children cannot outlive the step.
func invoke(ctx context.Context, workspace string, h *HarnessSpec, argv []string, out HarnessOutput, report ToolCallReporter) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workspace
	cmd.WaitDelay = killGrace
	// A nil Env would inherit the worker's environment and its credentials.
	cmd.Env = append([]string{}, h.Env...)
	setProcessGroup(cmd)
	if err := runAsHarnessUser(cmd, h.User); err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr tailBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start harness: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		out.Line(scanner.Bytes(), report)
	}
	_, _ = io.Copy(io.Discard, stdout)
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("harness exited: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func harnessPrompt(workspace string, req Request) string {
	var b strings.Builder
	b.WriteString(req.Mission)
	b.WriteString("\n\n## Rules\n\n")
	b.WriteString(projectScopedRules(workspace, req.ExtraRules))
	b.WriteString("\nDo not run git commands that change history, refs or configuration. Archie commits your changes.")
	if req.ReadOnly {
		b.WriteString("\nThis stage is read-only. Do not create, modify or delete any file.")
	}
	if patterns := slices.Concat(req.Protection.Suffixes, req.Protection.Globs); len(patterns) > 0 {
		b.WriteString("\nDo not modify files matching: " + strings.Join(patterns, ", "))
	}
	if len(req.Gate.Commands) > 0 {
		b.WriteString("\nYour work must pass these commands:")
		for _, c := range req.Gate.Commands {
			b.WriteString("\n- " + strings.Join(c.Argv, " "))
		}
	}
	if strings.TrimSpace(req.Notes) != "" {
		b.WriteString("\n\n## Notes\n\n" + req.Notes)
	}
	return b.String()
}

func runHarnessGate(ctx context.Context, commands []Command, workspace string) (string, error) {
	for _, c := range commands {
		if len(c.Argv) == 0 {
			continue
		}
		cmd := exec.CommandContext(ctx, c.Argv[0], c.Argv[1:]...)
		cmd.Dir = workspace
		output, err := cmd.CombinedOutput()
		switch {
		case c.ExpectFailure && err == nil:
			return fmt.Sprintf("[%s] expected this command to fail, but it passed:\n%s", c.Name, clipGate(output)),
				fmt.Errorf("gate %s: expected failure", c.Name)
		case !c.ExpectFailure && err != nil:
			return fmt.Sprintf("[%s] %s\n%s", c.Name, strings.Join(c.Argv, " "), clipGate(output)), fmt.Errorf("gate %s: %w", c.Name, err)
		}
	}
	return "", nil
}

func clipGate(out []byte) string {
	const limit = 4000
	s := strings.TrimSpace(string(out))
	if len(s) <= limit {
		return s
	}
	return s[:limit/2] + "\n...[truncated]...\n" + s[len(s)-limit/2:]
}

func park(res Result, stop, detail string) Result {
	res.Status, res.StopReason, res.Detail = StatusParked, stop, detail
	return res
}

func addUsage(a, b Usage) Usage {
	return Usage{
		PromptTokens:        a.PromptTokens + b.PromptTokens,
		CompletionTokens:    a.CompletionTokens + b.CompletionTokens,
		TotalTokens:         a.TotalTokens + b.TotalTokens,
		CachedTokens:        a.CachedTokens + b.CachedTokens,
		CacheCreationTokens: a.CacheCreationTokens + b.CacheCreationTokens,
	}
}

// repoState is what the runner compares across an invocation: every ref
// (HEAD included), the repository configuration, and a content hash of each
// path git reports as not clean.
type repoState struct {
	refs   map[string]string
	config string
	dirty  map[string]string
}

func snapshotRepo(workspace string) (repoState, error) {
	repo, err := git.PlainOpen(workspace)
	if err != nil {
		return repoState{}, err
	}
	state := repoState{refs: map[string]string{}, dirty: map[string]string{}}
	if head, err := repo.Storer.Reference(plumbing.HEAD); err == nil {
		state.refs[plumbing.HEAD.String()] = head.String()
	}
	iter, err := repo.References()
	if err != nil {
		return repoState{}, err
	}
	if err := iter.ForEach(func(ref *plumbing.Reference) error {
		state.refs[ref.Name().String()] = ref.String()
		return nil
	}); err != nil {
		return repoState{}, err
	}
	cfg, err := repo.Config()
	if err != nil {
		return repoState{}, err
	}
	raw, err := cfg.Marshal()
	if err != nil {
		return repoState{}, err
	}
	state.config = string(raw)
	wt, err := repo.Worktree()
	if err != nil {
		return repoState{}, err
	}
	status, err := wt.Status()
	if err != nil {
		return repoState{}, err
	}
	for path, st := range status {
		if st.Worktree == git.Unmodified && st.Staging == git.Unmodified {
			continue
		}
		state.dirty[path] = contentHash(filepath.Join(workspace, path))
	}
	return state, nil
}

func contentHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "absent"
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// changedPaths is every path whose state differs across the invocation. A
// path already dirty and left untouched is not a change of this run.
func changedPaths(before, after repoState) []string {
	var changed []string
	for path := range mergeKeys(before.dirty, after.dirty) {
		if before.dirty[path] != after.dirty[path] {
			changed = append(changed, path)
		}
	}
	slices.Sort(changed)
	return changed
}

func mergeKeys(a, b map[string]string) map[string]struct{} {
	keys := make(map[string]struct{}, len(a)+len(b))
	for k := range maps.Keys(a) {
		keys[k] = struct{}{}
	}
	for k := range maps.Keys(b) {
		keys[k] = struct{}{}
	}
	return keys
}

func policyViolation(before, after repoState, changes []string, req Request) string {
	if !maps.Equal(before.refs, after.refs) {
		var moved []string
		for name := range mergeKeys(before.refs, after.refs) {
			if before.refs[name] != after.refs[name] {
				moved = append(moved, name)
			}
		}
		slices.Sort(moved)
		return "the harness changed git refs: " + strings.Join(moved, ", ")
	}
	if before.config != after.config {
		return "the harness changed the git configuration"
	}
	if req.ReadOnly && len(changes) > 0 {
		return "the harness wrote in a read-only stage: " + strings.Join(changes, ", ")
	}
	if protected := protectionMatcher(req.Protection, false); protected != nil {
		var hit []string
		for _, path := range changes {
			if protected(path) {
				hit = append(hit, path)
			}
		}
		if len(hit) > 0 {
			return "the harness modified protected paths: " + strings.Join(hit, ", ")
		}
	}
	return ""
}

type silentOutput struct{}

func (silentOutput) Line([]byte, ToolCallReporter) {}
func (silentOutput) SessionID() string             { return "" }
func (silentOutput) Usage() Usage                  { return Usage{} }

// tailBuffer keeps the last stderrTailBytes written to it.
type tailBuffer struct{ buf bytes.Buffer }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf.Write(p)
	if extra := t.buf.Len() - stderrTailBytes; extra > 0 {
		t.buf.Next(extra)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return t.buf.String() }
