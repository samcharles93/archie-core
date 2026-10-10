package prbench

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/ai-sdk/agent"
	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview/bench"
)

// modelJudge asks one model, independent of the reviewer's, which posted
// comment catches each golden.
type modelJudge struct {
	runtime  *runtime.Runtime
	modelRef string
}

func (j *modelJudge) Judge(ctx context.Context, p bench.Problem, comments []prreview.ScoredFinding) ([]bench.Verdict, error) {
	provider, model, err := j.runtime.ChatProvider(ctx, j.modelRef)
	if err != nil {
		return nil, fmt.Errorf("resolve judge model %q: %w", j.modelRef, err)
	}
	var verdicts []bench.Verdict
	sub := agent.Subagent{
		Provider: provider,
		Model:    model,
		System:   judgeSystemPrompt,
		Tools:    core.ToolSet{"record_verdict": recordVerdictTool(len(p.Goldens), len(comments), &verdicts)},
		MaxSteps: 2*len(p.Goldens) + 3,
	}
	// The verdicts are the judgement, not the closing reply: a run that recorded
	// some and then ran out of steps is checked for completeness by the caller.
	if _, err := sub.Run(ctx, judgePrompt(p, comments)); err != nil && len(verdicts) == 0 {
		return nil, err
	}
	return verdicts, nil
}

const judgeSystemPrompt = `You score a code reviewer against known defects. You are given the pull request's known defects (goldens) and the numbered comments the reviewer posted.

For each golden, decide whether any posted comment identifies the same underlying defect: the same location and the same root cause. A comment about the same file but a different problem is not a match, and neither is a vague comment that would only be right by coincidence. Wording does not need to match.

Call record_verdict exactly once per golden. Pass the index of the matching comment, or -1 when no comment matches, and a one-sentence reason. When several comments match, pass the one that states the defect most directly.`

func judgePrompt(p bench.Problem, comments []prreview.ScoredFinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Pull request: %s\n\n<goldens>\n", p.PRURL)
	for i, g := range p.Goldens {
		fmt.Fprintf(&b, "[%d] (%s) %s\n", i, g.Severity, g.Comment)
	}
	b.WriteString("</goldens>\n\n<comments>\n")
	for i, c := range comments {
		fmt.Fprintf(&b, "[%d] %s:%d-%d (%s) %s\n%s\n\n", i, c.File, c.LineStart, c.LineEnd, c.Severity, c.Title, c.Body)
	}
	b.WriteString("</comments>\n")
	return b.String()
}

type verdictInput struct {
	Golden  int    `json:"golden" jsonschema:"description=Index of the golden this verdict is for."`
	Comment int    `json:"comment" jsonschema:"description=Index of the comment that identifies the same defect, or -1 when none does."`
	Reason  string `json:"reason" jsonschema:"description=One sentence on why the comment does or does not match."`
}

func recordVerdictTool(goldens, comments int, verdicts *[]bench.Verdict) *core.Tool {
	return core.NewTypedTool(
		"record_verdict",
		"Record whether a posted comment catches one golden. Call once per golden.",
		func(_ context.Context, in verdictInput) (string, error) {
			if in.Golden < 0 || in.Golden >= goldens {
				return fmt.Sprintf("record_verdict rejected: golden must be 0..%d", goldens-1), nil
			}
			if in.Comment < bench.NoComment || in.Comment >= comments {
				return fmt.Sprintf("record_verdict rejected: comment must be -1..%d", comments-1), nil
			}
			for _, v := range *verdicts {
				if v.Golden == in.Golden {
					return fmt.Sprintf("record_verdict rejected: golden %d already has a verdict", in.Golden), nil
				}
			}
			*verdicts = append(*verdicts, bench.Verdict{Golden: in.Golden, Comment: in.Comment, Reason: in.Reason})
			return "verdict recorded", nil
		},
	)
}
