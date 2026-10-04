package workflow

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/agentrun"
)

// The general repository step types. Each needs a repository.
const (
	RepoPrepareStepName   = "repo.prepare"
	RepoCommitStepName    = "repo.commit"
	RepoOpenPRStepName    = "repo.open-pr"
	GateRepositoryName    = "gate.repository"
	GateDiffSizeStepName  = "gate.diff-size"
	repoBranchTask        = "task"
	gateExpectTestFailure = "test-failure"
)

// RepoStepTypes contributes the general repository step types.
func RepoStepTypes() []StepType {
	return []StepType{
		{Name: RepoPrepareStepName, Factory: newRepoPrepareStage, Settings: repoPrepareSettings{}},
		{Name: RepoCommitStepName, Factory: newRepoCommitStage, Settings: repoCommitSettings{}},
		{Name: RepoOpenPRStepName, Factory: newRepoOpenPRStage, Settings: repoOpenPRSettings{}},
		{Name: GateRepositoryName, Factory: newGateRepositoryStage, Settings: gateRepositorySettings{}},
		{Name: GateDiffSizeStepName, Factory: func(yaml.Node) (Stage, error) {
			stage := StageDiffCap()
			stage.Name = GateDiffSizeStepName
			return stage, nil
		}},
	}
}

// decodeSettings decodes optional settings strictly.
func decodeSettings(step string, settings yaml.Node, into any) error {
	if settings.Kind == 0 {
		return nil
	}
	if err := settings.Decode(into); err != nil {
		return fmt.Errorf("%s: %w", step, err)
	}
	return nil
}

type repoPrepareSettings struct {
	// Branch is "fresh" (the default, a new branch from the base) or "task",
	// the branch an earlier run of this task pushed.
	Branch string `yaml:"branch" enum:"fresh,task" doc:"fresh starts a new branch from the base; task resumes the branch this task pushed."`
}

func newRepoPrepareStage(settings yaml.Node) (Stage, error) {
	var s repoPrepareSettings
	if err := decodeSettings(RepoPrepareStepName, settings, &s); err != nil {
		return Stage{}, err
	}
	var stage Stage
	switch s.Branch {
	case "", "fresh":
		stage = StagePrepareWorktree()
	case repoBranchTask:
		stage = StagePrepareWorktreeOnBranch()
	default:
		return Stage{}, fmt.Errorf("%s: settings.branch is fresh or task, not %q", RepoPrepareStepName, s.Branch)
	}
	stage.Name = RepoPrepareStepName
	return stage, nil
}

type repoCommitSettings struct {
	Message string `yaml:"message" doc:"The commit message."`
	// Reference is the verb that links the commit to the task's issue
	// ("Fixes", "Implements", "Refs"); empty links nothing.
	Reference string `yaml:"reference" enum:"Fixes,Implements,Refs" doc:"How the commit links to the task issue."`
	Push      bool   `yaml:"push" doc:"Push the branch after committing."`
	// IfEmpty is what an empty worktree does: "fail" (the default), "skip",
	// or "complete", which closes the issue and completes the workflow.
	IfEmpty string `yaml:"if_empty" title:"If nothing changed" enum:"fail,skip,complete" doc:"fail stops the run, skip moves on, complete closes the issue and ends the run."`
}

func newRepoCommitStage(settings yaml.Node) (Stage, error) {
	var s repoCommitSettings
	if err := decodeSettings(RepoCommitStepName, settings, &s); err != nil {
		return Stage{}, err
	}
	if strings.TrimSpace(s.Message) == "" {
		return Stage{}, fmt.Errorf("%s: settings.message is required", RepoCommitStepName)
	}
	switch s.IfEmpty {
	case "", "fail", "skip", "complete":
	default:
		return Stage{}, fmt.Errorf("%s: settings.if_empty is fail, skip or complete, not %q", RepoCommitStepName, s.IfEmpty)
	}
	return Stage{Name: RepoCommitStepName, Run: func(ctx context.Context, tc *TaskContext) error {
		message := s.Message
		if s.Reference != "" {
			message += commitIssueReference(s.Reference, tc.Task)
		}
		changed, err := tc.Trees.CommitAll(ctx, tc.Dir, message)
		if err != nil {
			return err
		}
		if !changed && !tc.BaselineFixed {
			switch s.IfEmpty {
			case "skip":
				return nil
			case "complete":
				return closeNoChangesIssue(ctx, tc)
			default:
				return fmt.Errorf("worktree has no changes to commit")
			}
		}
		if s.Push {
			if err := tc.Trees.Push(ctx, tc.Dir, tc.Branch); err != nil {
				return err
			}
			tc.captureChanges(ctx, capturedAfterCommitPush)
			return nil
		}
		tc.captureChanges(ctx, capturedAfterCommit)
		return nil
	}}, nil
}

type repoOpenPRSettings struct {
	Body string `yaml:"body" doc:"The pull request description."`
	// Review runs the PR review pipeline on the change first; a blocking
	// finding parks the task instead of opening the PR.
	Review bool `yaml:"review" doc:"Review the change first; a blocking finding parks the task instead of opening the PR."`
}

func newRepoOpenPRStage(settings yaml.Node) (Stage, error) {
	var s repoOpenPRSettings
	if err := decodeSettings(RepoOpenPRStepName, settings, &s); err != nil {
		return Stage{}, err
	}
	body := func(tc *TaskContext) string {
		return fmt.Sprintf("%s\n\n---\n*workflow: %s · %d iterations · %s*",
			s.Body, tc.Task.Workflow, tc.Task.Iterations, formatTokenUsage(tc.Task.TokensUsed, tc.RunUsage))
	}
	stage := StageOpenPR(body)
	if s.Review {
		stage = StagePRReviewAndOpenPR(body)
	}
	stage.Name = RepoOpenPRStepName
	return stage, nil
}

type gateRepositorySettings struct {
	// Expect is "pass" (the default) or "test-failure": every gate command
	// passes except the test command, which must fail.
	Expect string `yaml:"expect" enum:"pass,test-failure" doc:"pass requires every gate command to pass; test-failure requires the test command to fail."`
	// Repair lets the builder fix a failing gate before the step fails.
	Repair bool `yaml:"repair" doc:"Let the builder fix a failing gate before the step fails."`
}

// newGateRepositoryStage runs the repository's gate commands in the worktree.
// Its summary is the test command's output, so a later step can quote it.
func newGateRepositoryStage(settings yaml.Node) (Stage, error) {
	var s gateRepositorySettings
	if err := decodeSettings(GateRepositoryName, settings, &s); err != nil {
		return Stage{}, err
	}
	switch {
	case s.Expect != "" && s.Expect != "pass" && s.Expect != gateExpectTestFailure:
		return Stage{}, fmt.Errorf("%s: settings.expect is pass or test-failure, not %q", GateRepositoryName, s.Expect)
	case s.Repair && s.Expect == gateExpectTestFailure:
		return Stage{}, fmt.Errorf("%s: repair applies only when the gate is expected to pass", GateRepositoryName)
	}
	if s.Repair {
		stage := StageBaselineGate()
		stage.Name = GateRepositoryName
		return stage, nil
	}
	expectFailure := s.Expect == gateExpectTestFailure
	return Stage{Name: GateRepositoryName, Run: func(ctx context.Context, tc *TaskContext) error {
		gate := GateFromRepo(tc.Repo, tc.Cfg.Budgets)
		if expectFailure {
			gate = tddReproGate(tc.Repo, tc.Cfg.Budgets)
		}
		if len(gate.Commands) == 0 {
			return fmt.Errorf("repository %s has no gate commands", tc.Repo.FullName())
		}
		for _, command := range gate.Commands {
			out, err := runGateCommand(ctx, tc.Dir, command)
			tc.stepResult.Summary = clip(out, 4000)
			if err != nil {
				return err
			}
		}
		return nil
	}}, nil
}

func runGateCommand(ctx context.Context, dir string, command agentrun.Command) (string, error) {
	cmd := exec.CommandContext(ctx, command.Argv[0], command.Argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	name := strings.Join(command.Argv, " ")
	switch {
	case command.ExpectFailure && err == nil:
		return string(out), fmt.Errorf("%s passed, but it was expected to fail", name)
	case !command.ExpectFailure && err != nil:
		return string(out), fmt.Errorf("%s failed: %w", name, err)
	}
	return string(out), nil
}
