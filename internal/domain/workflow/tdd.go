package workflow

import (
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/agentrun"
)

// tddReproGate builds the inverted gate for the repro-tests stage:
// every command from repo.Gate runs normally except the last one (the
// test runner, by convention), which gets ExpectFailure  --  the repro
// must fail the tests to prove the bug exists.
func tddReproGate(repo config.Repo, budgets config.Budgets) agentrun.Gate {
	if len(repo.Gate) == 0 {
		return agentrun.Gate{}
	}
	cmds := make([]agentrun.Command, 0, len(repo.Gate))
	for _, argv := range repo.Gate {
		if len(argv) == 0 {
			continue
		}
		gc := agentrun.Command{Name: argv[0], Argv: argv}
		cmds = append(cmds, gc)
	}
	if len(cmds) > 0 {
		cmds[len(cmds)-1].ExpectFailure = true
	}
	return agentrun.Gate{
		Commands:               cmds,
		MaxConsecutiveFailures: budgets.GateMaxFailures,
	}
}
