package workflow

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/agentexec"
)

// CommandRunStepName is the step type that runs operator-authored argv in the
// task worktree at its position in a stored workflow definition.
//
// It is the stage half of the deleted.archie/stages/*.go, and it exists only
// because the maintainer accepted the command-step trust decision: stored
// control-plane settings already drive argv execution -- repository-policies'
// gate and preflight commands, and tool-settings' stdio MCP servers -- so a
// command step adds no trust tier that is not already live.
//
// It runs where the workflow engine runs, which is the task container:
// internal/app/agentworker is the only caller of Run
// (internal/app/agentworker/task_execution.go), and it is what cmd/archie-agent
// serves. The daemon never executes a stage, so this step is on the container
// path by construction rather than by a check it performs.
const CommandRunStepName = "command.run"

// commandRunLevelWarn makes a failing command advisory: it is reported and the
// workflow continues instead of parking.
const commandRunLevelWarn = "warn"

// commandRunSettings are the command.run step's settings.
type commandRunSettings struct {
	// Level is "error" (the default) or "warn". At "warn" a non-zero exit is
	// logged and the workflow continues, which is what makes the step usable
	// for a check the operator wants to see without being stopped by it.
	Level string `yaml:"level"`
	// Run is the commands to execute, in order. The first failure ends the
	// step, so a later command does not run after an earlier one failed.
	Run []commandRunCommand `yaml:"run"`
}

// commandRunCommand is one command in a command.run step's settings.
//
// It is a settings shape of its own rather than agentexec.Command itself
// because these settings are decoded from YAML and agentexec.Command carries
// wire (JSON) tags only. Reusing the wire struct here would silently drop
// expect_failure -- the one field whose loss is hardest to notice, because a
// command declared to fail would simply be reported as passing, and the
// definition would say one thing while the run did another.
//
// Every field is still handed to agentexec.Command before execution, so the two
// cannot mean different things; TestCommandRunCarriesEveryGateCommandField
// holds this shape to that struct's fields in both directions.
type commandRunCommand struct {
	Name          string   `yaml:"name"`
	Argv          []string `yaml:"argv"`
	ExpectFailure bool     `yaml:"expect_failure"`
}

// command converts the settings shape into the type the execution path takes,
// so command.run and the repository gate share one command meaning.
func (c commandRunCommand) command() agentexec.Command {
	return agentexec.Command{Name: c.Name, Argv: c.Argv, ExpectFailure: c.ExpectFailure}
}

// CommandRunStepType contributes the command.run step type.
func CommandRunStepType() StepType {
	return StepType{Name: CommandRunStepName, Factory: newCommandRunStage}
}

func newCommandRunStage(settings yaml.Node) (Stage, error) {
	if settings.Kind == 0 {
		return Stage{}, fmt.Errorf("%s: settings.run is required", CommandRunStepName)
	}
	var s commandRunSettings
	if err := settings.Decode(&s); err != nil {
		return Stage{}, fmt.Errorf("%s: %w", CommandRunStepName, err)
	}
	switch s.Level {
	case "", "error", commandRunLevelWarn:
	default:
		return Stage{}, fmt.Errorf("%s: settings.level %q (want error or warn)", CommandRunStepName, s.Level)
	}
	if len(s.Run) == 0 {
		return Stage{}, fmt.Errorf("%s: settings.run needs at least one command", CommandRunStepName)
	}
	commands := make([]agentexec.Command, 0, len(s.Run))
	for i := range s.Run {
		if len(s.Run[i].Argv) == 0 {
			return Stage{}, fmt.Errorf("%s: settings.run[%d].argv is required", CommandRunStepName, i)
		}
		// A command with no name would report a failure as "[]
		// <output>", so it borrows its program name rather than being
		// refused: the name is for the operator reading the park reason,
		// not a decision the definition has to make twice.
		if strings.TrimSpace(s.Run[i].Name) == "" {
			s.Run[i].Name = s.Run[i].Argv[0]
		}
		commands = append(commands, s.Run[i].command())
	}

	advisory := s.Level == commandRunLevelWarn
	return Stage{Name: CommandRunStepName, Run: func(ctx context.Context, tc *TaskContext) error {
		if tc.Dir == "" {
			return fmt.Errorf("%s: no workspace; run it after the step that prepares one", CommandRunStepName)
		}
		detail, err := runCommands(ctx, commands, tc.Dir)
		if err == nil {
			return nil
		}
		if advisory {
			if tc.Log != nil {
				tc.Log.Warn("command.run step failed; advisory, so the run continues", "detail", detail, "err", err)
			}
			return nil
		}
		// Park rather than return an error: a returned error fails the
		// attempt, while a park leaves the run for an operator to retry
		// after fixing what the command found. An outcome stops the
		// engine at this step, so later steps do not run.
		tc.Outcome = Outcome{
			Status: StatusParked,
			Detail: fmt.Sprintf("%s: %s", CommandRunStepName, detail),
		}
		return nil
	}}, nil
}

// runCommands executes argv commands in dir, in order, and returns the first
// failure's clipped output.
//
// It mirrors the repository gate's execution exactly
// (internal/agentexec/harness_runner.go's runHarnessGate): argv is executed
// directly rather than through a shell -- so the definition's argv list is the
// argument boundary, and no quoting is interpreted -- expect_failure inverts a
// command's result, and the output is clipped so one noisy command cannot fill
// a park reason. The two are separate implementations of one contract; a change
// to either is a change to both.
func runCommands(ctx context.Context, commands []agentexec.Command, dir string) (string, error) {
	for _, c := range commands {
		cmd := exec.CommandContext(ctx, c.Argv[0], c.Argv[1:]...)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		switch {
		case c.ExpectFailure && err == nil:
			return fmt.Sprintf("[%s] expected this command to fail, but it passed:\n%s", c.Name, clipCommandOutput(output)),
				fmt.Errorf("command %s: expected failure", c.Name)
		case !c.ExpectFailure && err != nil:
			return fmt.Sprintf("[%s] %s\n%s", c.Name, strings.Join(c.Argv, " "), clipCommandOutput(output)),
				fmt.Errorf("command %s: %w", c.Name, err)
		}
	}
	return "", nil
}

// clipCommandOutput bounds one command's output. The limit matches the gate's
// (agentexec's clipGate) so a failure reads the same wherever an operator
// encounters it.
func clipCommandOutput(out []byte) string {
	const limit = 4000
	s := strings.TrimSpace(string(out))
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "\n[output clipped]"
}
