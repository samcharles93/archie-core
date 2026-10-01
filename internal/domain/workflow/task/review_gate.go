package task

import (
	"encoding/json"

	"github.com/samcharles93/archie-core/internal/taskstate"
)

// ReviewGate is the operator-approval gate's whole conversation with the
// operator, encoded on the task's review_gate column
// (docs/prds/pr-review-operator-response.md).
//
// It lives with the task record it encodes, the way EncodeInputs does, for the
// same reason: the dashboard process renders and answers a gate, and it must
// not link the workflow engine that owns the finding type the pipeline reads.
// The findings therefore cross this document as opaque JSON beside the key an
// operator's selection names (ReviewGateFinding); only the pipeline decodes
// them.
//
// The offer half -- the findings, the head SHA, the pull request's identity
// and the workflow the wait resumes -- is written by the gate stage before it
// ends the run in waiting_human. The answer half -- the outcome, the finding
// keys an approve posts and the instructions a re-review carries -- is
// written back by the response path in the guarded requeue that clears the
// wait.
type ReviewGate struct {
	// Findings is the review the operator is answering. An approve keeps it
	// (the resumed run posts it, filtered by Selection); a re-review's
	// response clears it, because the resumed run recomputes the review from
	// scratch.
	Findings []ReviewGateFinding `json:"findings,omitempty"`
	// HeadSHA is the pull request head the findings were measured on, so the
	// review the resume posts anchors to the commit the operator reviewed.
	HeadSHA string `json:"head_sha,omitempty"`
	// Owner, Repo and PRNumber are the pull request's identity. They ride in
	// the document rather than being re-read from the task so the review the
	// operator approved posts against exactly the pull request it named.
	Owner    string `json:"owner,omitempty"`
	Repo     string `json:"repo,omitempty"`
	PRNumber int    `json:"pr_number,omitempty"`
	// Workflow is the workflow the wait resumes. It is recorded for the
	// operator's benefit; the requeue keeps the task's own workflow, which
	// this must agree with.
	Workflow string `json:"workflow,omitempty"`
	// Outcome is the operator's answer: GateApprove or GateRereview, empty
	// while the gate is still waiting.
	Outcome string `json:"outcome,omitempty"`
	// Selection is the finding keys an approve posts. Empty means all of the
	// offered findings.
	Selection []string `json:"selection,omitempty"`
	// Instructions is what the operator asked a re-review to focus on. The
	// PRD requires it for a re-review; the lens and reviewer missions read it.
	Instructions string `json:"instructions,omitempty"`
}

// ReviewGateFinding is one offered finding: its stable key, which an
// operator's selection names and the response path validates against, beside
// the finding's own JSON. The key is part of the document rather than derived
// here so the operator surface never has to know the finding's type.
type ReviewGateFinding struct {
	Key     string          `json:"key"`
	Finding json.RawMessage `json:"finding"`
}

// The gate's outcome values. They are the operator actions that answer a
// gate, spelled by the shared lifecycle vocabulary so a rename cannot leave
// the gate recording a decision no surface offers.
const (
	GateApprove  = string(taskstate.ActionApprove)
	GateRereview = string(taskstate.ActionRereview)
)

// MaxRereviewRounds bounds how many operator re-reviews one review gate
// grants, per the PRD's Decision 2. It is a constant in the domain, not a
// config knob, and the guarded store write enforces it in the row so two
// simultaneous re-reviews cannot spend a round the cap forbids.
const MaxRereviewRounds = 2

// Approved reports whether the operator's answer was an approve.
func (g ReviewGate) Approved() bool { return g.Outcome == GateApprove }

// Offered reports whether the document holds a review the operator was shown.
func (g ReviewGate) Offered() bool { return len(g.Findings) > 0 }

// OfferedKeys returns the keys of every offered finding, in offer order.
func (g ReviewGate) OfferedKeys() []string {
	keys := make([]string, len(g.Findings))
	for i, entry := range g.Findings {
		keys[i] = entry.Key
	}
	return keys
}

// Posted returns the offered findings an approve posts, filtered by the
// operator's selection. An absent selection means all of them, in the order
// the findings were offered.
func (g ReviewGate) Posted() []ReviewGateFinding {
	if !g.Approved() {
		return nil
	}
	if len(g.Selection) == 0 {
		return g.Findings
	}
	selected := make(map[string]struct{}, len(g.Selection))
	for _, key := range g.Selection {
		selected[key] = struct{}{}
	}
	out := make([]ReviewGateFinding, 0, len(g.Findings))
	for _, entry := range g.Findings {
		if _, ok := selected[entry.Key]; ok {
			out = append(out, entry)
		}
	}
	return out
}

// EncodeReviewGate renders the document for the review_gate column. The
// package that owns the column owns its encoding, so neither the store nor the
// wire parses it. Encoding the zero document yields "" rather than "{}": an
// empty column is the format for "this task never reached the gate".
func EncodeReviewGate(g ReviewGate) string {
	if gateIsZero(g) {
		return ""
	}
	data, err := json.Marshal(g)
	if err != nil {
		// ReviewGate holds only strings, ints and JSON already validated by
		// encoding/json; a marshal failure would be a programming error, and
		// the honest encoding of "there is no document" is empty.
		return ""
	}
	return string(data)
}

// gateIsZero reports whether the document carries nothing at all. The struct
// holds slices, so it cannot be compared with ==.
func gateIsZero(g ReviewGate) bool {
	return g.Outcome == "" && g.Instructions == "" && g.Selection == nil &&
		g.HeadSHA == "" && g.Owner == "" && g.Repo == "" && g.PRNumber == 0 &&
		g.Workflow == "" && g.Findings == nil
}

// DecodeReviewGate parses a stored review_gate document. An empty or
// unparseable value decodes to ok=false: a task that never reached the gate,
// or a row from before the column existed, must not be mistaken for an offer.
func DecodeReviewGate(stored string) (ReviewGate, bool) {
	if stored == "" {
		return ReviewGate{}, false
	}
	var g ReviewGate
	if err := json.Unmarshal([]byte(stored), &g); err != nil {
		return ReviewGate{}, false
	}
	return g, true
}
