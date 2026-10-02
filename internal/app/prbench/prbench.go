// Package prbench wires the PR reviewer benchmark: the Martian Code-Review-Bench
// problems, a reviewer, and a judge model on the built-in runner.
package prbench

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/app/archied"
	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview/bench"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

// martian holds the 38 runnable problems of the Martian Code-Review-Bench
// offline set: those whose pull request still exists upstream.
//
//go:embed martian.json
var martian []byte

// Problems returns the benchmark problems, optionally narrowed to ids.
func Problems(ids []string) ([]bench.Problem, error) {
	var all []bench.Problem
	if err := json.Unmarshal(martian, &all); err != nil {
		return nil, fmt.Errorf("decode benchmark problems: %w", err)
	}
	if len(ids) == 0 {
		return all, nil
	}
	var out []bench.Problem
	for _, id := range ids {
		i := slices.IndexFunc(all, func(p bench.Problem) bool { return p.ID == id })
		if i < 0 {
			return nil, fmt.Errorf("no benchmark problem %q", id)
		}
		out = append(out, all[i])
	}
	return out, nil
}

// Options configures one benchmark run.
type Options struct {
	Config        string
	Fixtures      string
	JudgeModel    string
	ReviewModel   string
	ClassifyModel string
	GitHubToken   string
	Resume        string
	Out           string
	Problems      []string
	Concurrency   int
}

// Run reviews and judges the selected problems, writes one result file per
// problem and a summary to Options.Out, and prints the summary to w.
func Run(ctx context.Context, opts Options, w io.Writer) (bench.Summary, error) {
	problems, err := Problems(opts.Problems)
	if err != nil {
		return bench.Summary{}, err
	}
	doc, err := configuration.New(nil).File(opts.Config)
	if err != nil {
		return bench.Summary{}, err
	}
	if err := archied.ResolveProviders(&doc.Config, slog.Default()); err != nil {
		return bench.Summary{}, err
	}
	rt := agentexec.NewRuntime(agentexec.ProvidersFromConfig(doc.Config.Providers))
	if rt == nil {
		return bench.Summary{}, fmt.Errorf("%s configures no providers for the judge", opts.Config)
	}
	judge := &modelJudge{runtime: rt, modelRef: opts.JudgeModel}
	reviewer, err := newBenchmarkReviewer(opts, doc.Config, rt)
	if err != nil {
		return bench.Summary{}, err
	}

	results, err := runReviews(ctx, problems, reviewer, judge, opts)
	if err != nil {
		return bench.Summary{}, err
	}
	summary := bench.Summarize(results)
	if err := writeResults(opts.Out, results, summary, opts); err != nil {
		return summary, err
	}
	for _, r := range results {
		if r.Err != "" {
			fmt.Fprintf(w, "%s: %s\n", r.Problem.ID, r.Err)
		}
	}
	fmt.Fprintf(w, "problems %d (failed %d)  recall %.3f (%d/%d)  golden-only precision %.3f (%d/%d)\n",
		summary.Problems, summary.Failed, summary.Recall, summary.Hits, summary.Goldens,
		summary.Precision, summary.Matched, summary.Comments)
	if summary.Failed != 0 {
		return summary, fmt.Errorf("%d benchmark problem(s) failed; results are incomplete", summary.Failed)
	}
	if opts.Fixtures == "" && len(opts.Problems) == 0 && summary.Recall < 0.70 {
		return summary, fmt.Errorf("golden micro-recall %.3f is below 0.70", summary.Recall)
	}
	return summary, nil
}

func runReviews(ctx context.Context, problems []bench.Problem, reviewer bench.Reviewer, judge bench.Judge, opts Options) ([]bench.Result, error) {
	results, pending, err := resumeResults(problems, opts)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(opts.Out, "results"), 0o755); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(opts.Out, "run.json"), metadata(opts)); err != nil {
		return nil, err
	}
	var saveMu sync.Mutex
	var saveErr error
	newResults := bench.Run(ctx, pending, reviewer, judge, opts.Concurrency, func(r bench.Result) {
		if err := writeJSON(filepath.Join(opts.Out, "results", r.Problem.ID+".json"), r); err != nil {
			saveMu.Lock()
			saveErr = err
			saveMu.Unlock()
		}
	})
	if saveErr != nil {
		return nil, fmt.Errorf("save benchmark result: %w", saveErr)
	}
	byID := make(map[string]bench.Result, len(newResults))
	for _, r := range newResults {
		byID[r.Problem.ID] = r
	}
	for i, r := range results {
		if r.Problem.ID == "" {
			results[i] = byID[problems[i].ID]
		}
	}
	return results, nil
}

func writeResults(dir string, results []bench.Result, summary bench.Summary, opts Options) error {
	if err := os.MkdirAll(filepath.Join(dir, "results"), 0o755); err != nil {
		return err
	}
	for _, r := range results {
		if err := writeJSON(filepath.Join(dir, "results", r.Problem.ID+".json"), r); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(dir, "summary.json"), summary); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, "run.json"), metadata(opts))
}

type runMetadata struct {
	ReviewModel         string `json:"review_model"`
	ClassificationModel string `json:"classification_model"`
	JudgeModel          string `json:"judge_model"`
	Live                bool   `json:"live"`
}

func metadata(opts Options) runMetadata {
	return runMetadata{opts.ReviewModel, opts.ClassifyModel, opts.JudgeModel, opts.Fixtures == ""}
}

func resumeResults(problems []bench.Problem, opts Options) ([]bench.Result, []bench.Problem, error) {
	results := make([]bench.Result, len(problems))
	if opts.Resume == "" {
		return results, problems, nil
	}
	data, err := os.ReadFile(filepath.Join(opts.Resume, "run.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("read benchmark resume metadata: %w", err)
	}
	var previous runMetadata
	if err := json.Unmarshal(data, &previous); err != nil {
		return nil, nil, fmt.Errorf("decode benchmark resume metadata: %w", err)
	}
	if previous != metadata(opts) {
		return nil, nil, fmt.Errorf("benchmark resume models or mode differ from this run")
	}
	var pending []bench.Problem
	for i, p := range problems {
		data, err := os.ReadFile(filepath.Join(opts.Resume, "results", p.ID+".json"))
		if os.IsNotExist(err) {
			pending = append(pending, p)
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read benchmark result %s: %w", p.ID, err)
		}
		var r bench.Result
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, nil, fmt.Errorf("decode benchmark result %s: %w", p.ID, err)
		}
		if !reflect.DeepEqual(r.Problem, p) {
			return nil, nil, fmt.Errorf("benchmark result %s belongs to a different problem", p.ID)
		}
		if r.Err != "" {
			pending = append(pending, p)
			continue
		}
		if len(r.Verdicts) != len(p.Goldens) {
			return nil, nil, fmt.Errorf("benchmark result %s is incomplete or belongs to a different problem", p.ID)
		}
		results[i] = r
	}
	return results, pending, nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".prbench-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
