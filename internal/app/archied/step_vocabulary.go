package archied

import (
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/workflowsteps"
)

// stepVocabulary builds the workflow step vocabulary this archied process
// resolves workflow definitions against: the shared provider set
// (internal/infrastructure/workflowsteps), registered here at the composition
// root, before the first resolution.
//
// Both archied roots call it -- RunStateStore (the State Store's validating
// side) and openStateStoreAdapter (the daemon's control-plane client) -- so
// neither can register a vocabulary of its own. It is a named function so that
// step_vocabulary_test.go can fail if it stops using the shared provider set;
// the roots themselves own a SQLite file and dial gRPC, so the test asserts
// they call it by parsing their bodies.
//
// Agreement is within one build: the State Store and archie-agent are
// separately deployed binaries, so a State Store built from newer source than
// the agent it dispatches to can still disagree, and only a matching deploy
// fixes that.
func stepVocabulary() (*workflow.Manager, error) { return workflowsteps.NewManager() }
