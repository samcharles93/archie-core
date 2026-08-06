package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/store"
)

// Feasibility is the feature-request workflow: assess the request
// against the project's direction, close it with reasons when it
// doesn't fit, otherwise produce a PRD, deliver it to Sam (issue
// comment + notify webhook), and hand the task to waiting_human. The
// daemon watches for Sam's reply and  --  LLM-judged, not keyword-matched  --
// requeues approved features under the implement workflow or closes
// rejected ones. Routed via the "feature" label.
func Feasibility() Workflow {
	return Workflow{
		Name: "feasibility",
		Stages: []Stage{
			StagePrepareWorktree(), // read-only stages still need the checkout

			AgentStage{
				Name:         "assess",
				Role:         "planner",
				ReadOnly:     true,
				CaptureTools: decideCaptureTools,
				Mission: func(tc *TaskContext) string {
					return fmt.Sprintf(
						"Assess whether this feature request fits the project %s.\n\n"+
							"%s\n\n"+
							"Read the repository's AGENT.md, ROADMAP.md, and README (whichever exist) plus "+
							"enough code to judge scope and architectural fit. Then call the decide tool "+
							"EXACTLY ONCE with fit=true or fit=false and your reasons, and afterwards call "+
							"finish with status \"passed\" summarising the assessment.",
						tc.Repo.FullName(), taskPromptBlock(tc.Task),
					)
				},
				OnResult: func(tc *TaskContext, res agentexec.Result) error {
					calls := res.Captures["decide"]
					if len(calls) != 1 {
						return fmt.Errorf("assess stage called the decide tool %d times (want exactly once)", len(calls))
					}
					var captured struct {
						Fit     *bool  `json:"fit"`
						Reasons string `json:"reasons"`
					}
					if err := json.Unmarshal(calls[0], &captured); err != nil {
						return fmt.Errorf("decode feasibility decision: %w", err)
					}
					if captured.Fit == nil {
						return fmt.Errorf("assess stage decision has no boolean fit value")
					}
					if captured.Reasons == "" {
						return fmt.Errorf("assess stage decision has no reasons")
					}
					tc.decision = &decision{Fit: *captured.Fit, Reasons: captured.Reasons}
					if !tc.decision.Fit {
						if tc.Task.IsForgeBacked() {
							if err := tc.Forge.CloseIssue(context.Background(), tc.Task.Owner, tc.Task.Repo, tc.Task.IssueNumber, ""); err != nil {
								return err
							}
						}
						tc.Outcome = Outcome{Status: store.StatusClosedWontDo, Detail: tc.decision.Reasons}
					}
					return nil
				},
			}.Stage(),

			AgentStage{
				Name:     "prd",
				Role:     "planner",
				ReadOnly: true,
				Mission: func(tc *TaskContext) string {
					return fmt.Sprintf(
						"Write a PRD for this accepted feature request on %s.\n\n"+
							"%s\n\n<assessment>\n%s\n</assessment>\n\n"+
							"Explore the affected code surface, then call finish with status \"passed\" and the "+
							"PRD as the summary: problem, proposed solution, files/components affected, "+
							"acceptance criteria, explicit non-goals, and estimated diff size. It will be read "+
							"by a human deciding whether to green-light implementation.",
						tc.Repo.FullName(), taskPromptBlock(tc.Task), tc.decision.Reasons,
					)
				},
				OnResult: func(tc *TaskContext, res agentexec.Result) error {
					tc.Task.Plan = res.Summary
					return nil
				},
			}.Stage(),

			// Persist the PRD and block on Sam. Messaging and the Web UI are
			// the decision surfaces; the forge issue is not a chat log.
			{Name: "deliver", Run: func(ctx context.Context, tc *TaskContext) error {
				notify(ctx, tc, "feasibility_prd")
				tc.Outcome = Outcome{Status: store.StatusWaitingHuman, Detail: "PRD delivered, awaiting go/no-go"}
				return nil
			}},
		},
	}
}

// decision is the assess stage's structured verdict.
type decision struct {
	Fit     bool   `json:"fit"`
	Reasons string `json:"reasons"`
}

// decideCaptureTools gives the assess agent a structured verdict tool. Its
// arguments cross the execution boundary as data and are applied by OnResult.
func decideCaptureTools(*TaskContext) []agentexec.CaptureTool {
	params := json.RawMessage(`{
		"type": "object",
		"properties": {
			"fit": {"type": "boolean", "description": "true: fits the project and is worth a PRD. false: close as won't-do."},
			"reasons": {"type": "string", "description": "The rationale, written for the human who filed the request."}
		},
		"required": ["fit", "reasons"]
	}`)
	return []agentexec.CaptureTool{{
		Name: "decide", Description: "Record the feasibility verdict. Call exactly once, before finish.",
		Parameters: params, RequiredFields: []string{"fit", "reasons"},
		NonEmptyStrings: []string{"reasons"}, BooleanFields: []string{"fit"}, MaxCalls: 1,
	}}
}

// notify POSTs a JSON payload to the configured webhook (n8n turns it
// into an email). Best-effort: failures log, the issue comment is the
// authoritative channel.
func notify(ctx context.Context, tc *TaskContext, kind string) {
	url := tc.Cfg.Notify.Webhook
	if url == "" {
		return
	}
	fields := map[string]any{
		"type": kind, "repo": tc.Repo.FullName(), "task_id": tc.Task.ID,
		"source": tc.Task.Source, "identity": tc.Task.Identity,
		"title": tc.Task.Title, "prd": tc.Task.Plan,
	}
	if tc.Task.IsForgeBacked() {
		fields["issue"] = tc.Task.IssueNumber
		fields["issue_url"] = fmt.Sprintf("%s/%s/issues/%d",
			tc.Cfg.Forge.Host, tc.Repo.FullName(), tc.Task.IssueNumber)
	}
	payload, _ := json.Marshal(fields)
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		tc.Log.Warn("notify webhook request build failed", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		tc.Log.Warn("notify webhook failed", "err", err)
		return
	}
	if err := resp.Body.Close(); err != nil {
		tc.Log.Warn("close notify webhook response", "err", err)
	}
}
