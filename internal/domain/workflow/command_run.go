package workflow

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/agentrun"
)

// CommandRunStepName is the step type that runs operator-authored argv in the
// task worktree, inside the agent container.
const CommandRunStepName = "command.run"

// commandRunLevelWarn makes a failing command advisory: it is reported and the
// workflow continues instead of parking.
const commandRunLevelWarn = "warn"

// commandRunSettings are the command.run step's settings.
type commandRunSettings struct {
	// Level is "error" (the default) or "warn". At "warn" a non-zero exit is
	// logged and the workflow continues, which is what makes the step usable
	// for a check the operator wants to see without being stopped by it.
	Level string `yaml:"level" enum:"error,warn" doc:"error stops the run on a failing command; warn reports it and continues."`
	// Run is the commands to execute, in order. The first failure ends the
	// step, so a later command does not run after an earlier one failed.
	Run []commandRunCommand `yaml:"run" doc:"The commands to run in order; the first failure ends the step."`
}

// commandRunCommand is one command in a command.run step's settings. It is
// the YAML shape of agentrun.Command.
type commandRunCommand struct {
	Name          string   `yaml:"name"`
	Argv          []string `yaml:"argv" doc:"The program and its arguments."`
	ExpectFailure bool     `yaml:"expect_failure" title:"Expect failure" doc:"The command must exit non-zero."`
}

// command converts the settings shape into the type the execution path takes,
// so command.run and the repository gate share one command meaning.
func (c commandRunCommand) command() agentrun.Command {
	return agentrun.Command{Name: c.Name, Argv: c.Argv, ExpectFailure: c.ExpectFailure}
}

// CommandRunStepType contributes the command.run step type.
func CommandRunStepType() StepType {
	return StepType{Name: CommandRunStepName, Factory: newCommandRunStage, Settings: commandRunSettings{}}
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
	commands := make([]agentrun.Command, 0, len(s.Run))
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
		// Return the failure, with the command's clipped output as its
		// text: the shared step wrapper applies retry and on_failure, and
		// the engine parks with this message. Setting a parked outcome
		// here instead would stop the step before either control ran.
		return errors.New(detail)
	}}, nil
}

// runCommands executes argv commands in dir, in order, without a shell, and
// returns the first failure's clipped output. ExpectFailure inverts a
// command's result.
func runCommands(ctx context.Context, commands []agentrun.Command, dir string) (string, error) {
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
