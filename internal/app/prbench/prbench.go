// Package prbench wires the PR reviewer benchmark: the Martian Code-Review-Bench
// problems, a reviewer, and a judge model on the built-in runner.
package prbench

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

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
	Config      string
	Fixtures    string
	JudgeModel  string
	Out         string
	Problems    []string
	Concurrency int
}

// Run reviews and judges the selected problems, writes one result file per
// problem and a summary to Options.Out, and prints the summary to w.
func Run(ctx context.Context, opts Options, w io.Writer) (bench.Summary, error) {
	if opts.Fixtures == "" {
		return bench.Summary{}, errors.New("no reviewer: pass a findings fixture directory")
	}
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

	results := bench.Run(ctx, problems, fixtureReviewer{dir: opts.Fixtures}, judge, opts.Concurrency)
	summary := bench.Summarize(results)
	if err := writeResults(opts.Out, results, summary); err != nil {
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
	return summary, nil
}

func writeResults(dir string, results []bench.Result, summary bench.Summary) error {
	if err := os.MkdirAll(filepath.Join(dir, "results"), 0o755); err != nil {
		return err
	}
	for _, r := range results {
		if err := writeJSON(filepath.Join(dir, "results", r.Problem.ID+".json"), r); err != nil {
			return err
		}
	}
	return writeJSON(filepath.Join(dir, "summary.json"), summary)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
