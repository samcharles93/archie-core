// Package prbench runs the PR reviewer benchmark: the Martian Code-Review-Bench
// problems, the production review pipeline, and a judge model, all on the
// providers and model aliases the instance already configures.
package prbench

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/agentexec/modelloop"
	"github.com/samcharles93/archie-core/internal/app/servicekit"
	"github.com/samcharles93/archie-core/internal/buildinfo"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview/bench"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/prsource"
)

// acceptanceRecall is the PRD's golden micro-recall bar for a full run.
const acceptanceRecall = 0.70

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

// Options configures one benchmark run. Model and Judge each name a model
// alias of the config or a provider/model ref; empty means the "default" alias.
type Options struct {
	Config      string
	Model       string
	Judge       string
	OutDir      string
	Problems    []string
	Concurrency int
}

// Run reviews and judges the selected problems, writes the report into
// Options.OutDir, and prints it to w.
func Run(ctx context.Context, opts Options, w io.Writer) (bench.Summary, error) {
	problems, err := Problems(opts.Problems)
	if err != nil {
		return bench.Summary{}, err
	}
	doc, err := configuration.New(nil).File(opts.Config)
	if err != nil {
		return bench.Summary{}, err
	}
	cfg := doc.Config
	if err := servicekit.ResolveProviders(&cfg, slog.Default()); err != nil {
		return bench.Summary{}, err
	}
	reviewRef, err := resolveRef(cfg.Models, opts.Model)
	if err != nil {
		return bench.Summary{}, err
	}
	judgeRef, err := resolveRef(cfg.Models, opts.Judge)
	if err != nil {
		return bench.Summary{}, err
	}
	rt := modelloop.NewRuntime(agentexec.ProvidersFromConfig(cfg.Providers), cfg.ModelLimits)
	if rt == nil {
		return bench.Summary{}, fmt.Errorf("%s configures no providers", opts.Config)
	}
	// The pipeline resolves its model through the default alias.
	cfg.Models = map[string]string{config.DefaultModelAlias: reviewRef}
	cfg.Review.ApproveBeforePost = false
	cfg.Review.PrecisionGate = false
	reviewer := &liveReviewer{cfg: cfg, agent: modelloop.NewLoopRunner(rt, slog.Default()), source: prsource.New(newPublicGitHub())}
	judge := &modelJudge{runtime: rt, modelRef: judgeRef}

	results := bench.Run(ctx, problems, reviewer, judge, opts.Concurrency)
	summary := bench.Summarize(results)
	report := render(results, summary, reviewRef, judgeRef, len(opts.Problems) == 0)
	path, err := writeReport(opts.OutDir, report)
	if err != nil {
		return summary, err
	}
	fmt.Fprint(w, report)
	fmt.Fprintf(w, "\nreport written to %s\n", path)
	if summary.Failed != 0 {
		return summary, fmt.Errorf("%d benchmark problem(s) failed; results are incomplete", summary.Failed)
	}
	return summary, nil
}

// Main is the `archied prbench` subcommand.
func Main(args []string, stdout, stderr io.Writer) int {
	var opts Options
	var problems string
	flags := flag.NewFlagSet("archied prbench", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.Config, "config", configuration.DefaultConfigPath(), "config file whose providers and model aliases serve the run")
	flags.StringVar(&opts.Model, "model", "", "model alias or provider/model for the reviewer (default: the default alias)")
	flags.StringVar(&opts.Judge, "judge-model", "", "model alias or provider/model for the judge (default: the default alias; use a different model from the reviewer's for an independent judge)")
	flags.StringVar(&opts.OutDir, "out", "docs/benchmarks", "directory the report is written to")
	flags.StringVar(&problems, "problems", "", "comma-separated problem ids for a partial run (default: all 38)")
	flags.IntVar(&opts.Concurrency, "concurrency", 4, "problems reviewed at once")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if problems != "" {
		opts.Problems = strings.Split(problems, ",")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if _, err := Run(ctx, opts, stdout); err != nil {
		fmt.Fprintln(stderr, "archied prbench:", err)
		return 1
	}
	return 0
}

// resolveRef accepts a provider/model ref as is and otherwise looks the alias up.
func resolveRef(aliases map[string]string, v string) (string, error) {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "/") {
		return v, nil
	}
	return config.ResolveModel(aliases, v, config.PurposeAgent)
}

func render(results []bench.Result, s bench.Summary, reviewRef, judgeRef string, full bool) string {
	var b strings.Builder
	scope, verdict := "partial run (not the acceptance run)", ""
	if full {
		scope = "full run"
		verdict = fmt.Sprintf("\nAcceptance (recall >= %.2f): %s\n", acceptanceRecall, passFail(s.Failed == 0 && s.Recall >= acceptanceRecall))
	}
	fmt.Fprintf(&b, "# PR review benchmark\n\nDate: %s\nArchie version: %s\nReview model: %s\nJudge model: %s\nScope: %s\n\n",
		time.Now().UTC().Format("2006-01-02"), buildinfo.Version, reviewRef, judgeRef, scope)
	fmt.Fprintf(&b, "Problems %d (failed %d)  recall %.3f (%d/%d)  golden-only precision %.3f (%d/%d)\n%s\n",
		s.Problems, s.Failed, s.Recall, s.Hits, s.Goldens, s.Precision, s.Matched, s.Comments, verdict)
	b.WriteString("| Problem | Recall | Comments | Notes |\n| --- | --- | --- | --- |\n")
	for _, r := range results {
		recall, notes := fmt.Sprintf("%d/%d", r.Hits(), len(r.Problem.Goldens)), ""
		switch {
		case r.Err != "":
			recall, notes = "-", "failed: "+strings.ReplaceAll(r.Err, "|", "/")
		case len(r.SkippedPhases) != 0:
			notes = "skipped phases: " + strings.Join(r.SkippedPhases, ", ")
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %s |\n", r.Problem.ID, recall, len(r.Comments), notes)
	}
	return b.String()
}

func passFail(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

func writeReport(dir, report string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "pr-review-"+time.Now().UTC().Format("20060102-150405")+".md")
	return path, os.WriteFile(path, []byte(report), 0o644)
}
