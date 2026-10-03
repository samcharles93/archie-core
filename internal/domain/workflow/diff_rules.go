package workflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
)

// DiffRulesStepName is the step type that matches declarative rules against
// the lines the committed change adds:
//
//	steps:
//	  - type: gate.diff-rules
//	    settings:
//	      rules:
//	        - id: no-new-panic
//	          level: error
//	          path: '\.go$'
//	          pattern: 'panic\('
//	          message: new panic() call, use error returns instead
//
// The diff is the branch's commits against the base branch, so the step must
// follow the step that commits. An error-level match parks the run with the
// findings; a warn-level match is logged.
const DiffRulesStepName = "gate.diff-rules"

// DiffRuleLevel values. An error-level finding parks the run; a warn-level
// finding is advisory and never blocks.
const (
	DiffRuleLevelError = "error"
	DiffRuleLevelWarn  = "warn"
)

// diffRuleDetailBytes bounds how much of a park Detail the rendered findings
// occupy, matching the store's own park-reason cap headroom (see
// parkDetailBytes in prreview_own_pr.go).
const diffRuleDetailBytes = 4000

// DiffRule is one declarative rule applied to the added lines of the committed
// diff. Every field is an operator setting, so a rule is reviewable, versioned
// and audited with the definition it lives in -- none of which was true of the
// interpreted Go this replaces.
type DiffRule struct {
	// ID names the rule in the run's log and in the park reason. Optional;
	// without one a finding is still attributable to its file and line, not to
	// the rule that reported it.
	ID string `yaml:"id"`
	// Level is DiffRuleLevelError or DiffRuleLevelWarn, and is required: a rule
	// that does not say whether it blocks is refused rather than defaulted.
	Level string `yaml:"level"`
	// Pattern is the RE2 expression matched against each added line's text
	// (without its leading "+"). Required.
	Pattern string `yaml:"pattern"`
	// Path, when set, is an RE2 expression the changed file's path must match
	// for the rule to apply, e.g. '\.go$'. Optional.
	Path string `yaml:"path"`
	// ExcludePath, when set, is an RE2 expression the changed file's path must
	// NOT match for the rule to apply, e.g. '_test\.go$'. Optional, and the
	// only way to spare one class of file: RE2 has no lookahead.
	ExcludePath string `yaml:"exclude_path"`
	// Message is what the operator reads when the rule fires. Required.
	Message string `yaml:"message"`
}

// diffRulesSettings is the step's whole settings surface. It exists as a type
// so the decode is strict: a settings key the step does not read is refused
// where the definition is validated, not ignored at run time.
type diffRulesSettings struct {
	Rules []DiffRule `yaml:"rules"`
}

// DiffRulesStepType returns the registered step type for DiffRulesStepName, the
// way the shipped stages are declared: the factory builds the stage the
// executing side runs from the settings the validating side accepted.
func DiffRulesStepType() StepType {
	return StepType{Name: DiffRulesStepName, Factory: newDiffRulesStage}
}

// newDiffRulesStage decodes and compiles the step's settings, refusing every
// contribution it cannot apply. This runs where the definition is validated and
// again where it is compiled, so a stored definition that cannot be honoured
// fails closed instead of loading and then quietly applying nothing.
func newDiffRulesStage(settings yaml.Node) (Stage, error) {
	rules, err := decodeDiffRules(settings)
	if err != nil {
		return Stage{}, err
	}
	return Stage{Name: DiffRulesStepName, Run: func(ctx context.Context, tc *TaskContext) error {
		return runDiffRules(ctx, tc, rules)
	}}, nil
}

func runDiffRules(ctx context.Context, tc *TaskContext, rules []compiledDiffRule) error {
	if tc.Dir == "" {
		return fmt.Errorf("%s: no worktree to diff: this step must follow the one that prepares it", DiffRulesStepName)
	}
	base := tc.Repo.BaseBranch()
	diff, err := tc.Trees.Diff(ctx, tc.Dir, base)
	if err != nil {
		return fmt.Errorf("%s: diff against %s: %w", DiffRulesStepName, base, err)
	}

	added := addedDiffLines(diff)
	// The count is the run's own evidence of what the rules read: without it, a
	// step that read nothing and a step whose rules matched nothing leave the
	// same trace.
	tc.Log.Info("diff rules read the committed change", "base", base, "added_lines", len(added), "rules", len(rules))
	if diff == "" {
		if err := refuseAChangeThatIsNotCommitted(ctx, tc); err != nil {
			return err
		}
	}

	var blocking []diffFinding
	for _, finding := range matchDiffRules(rules, added) {
		logRuleFinding(tc, finding)
		if finding.Level == DiffRuleLevelError {
			blocking = append(blocking, finding)
		}
	}
	if len(blocking) > 0 {
		// A blocking finding is a verdict about the change, not a failure of the
		// run: the task parks with what was found, the way the adversarial
		// review parks with its findings, so an operator reads why rather than
		// an execution error that hides it.
		tc.Outcome = Outcome{Status: StatusParked, Detail: renderDiffRuleFindings(blocking)}
	}
	return nil
}

// uncommittedChangeReporter reports whether a worktree holds uncommitted,
// unstaged or untracked work.
type uncommittedChangeReporter interface {
	HasUncommittedChanges(ctx context.Context, dir string) (bool, error)
}

// refuseAChangeThatIsNotCommitted fails the step when no committed change was
// read but the worktree still holds work, or when the worktree cannot say.
func refuseAChangeThatIsNotCommitted(ctx context.Context, tc *TaskContext) error {
	const follow = "this step must follow the step that commits the change"

	reporter, ok := tc.Trees.(uncommittedChangeReporter)
	if !ok {
		return fmt.Errorf("%s: no committed change to check and this worktree implementation cannot report uncommitted work: %s", DiffRulesStepName, follow)
	}
	uncommitted, err := reporter.HasUncommittedChanges(ctx, tc.Dir)
	if err != nil {
		return fmt.Errorf("%s: read uncommitted work: %w", DiffRulesStepName, err)
	}
	if !uncommitted {
		return nil
	}
	return fmt.Errorf("%s: no committed change to check but the worktree holds uncommitted work: %s", DiffRulesStepName, follow)
}

// logRuleFinding logs a finding as a warning when it blocks and as info when
// advisory, with the rule's declared level as rule_level.
func logRuleFinding(tc *TaskContext, finding diffFinding) {
	attributes := []any{
		"rule_level", finding.Level,
		"rule", finding.Rule,
		"file", finding.Path,
		"line", finding.Line,
		"message", finding.Message,
	}
	if finding.Level == DiffRuleLevelError {
		tc.Log.Warn("diff rule finding", attributes...)
		return
	}
	tc.Log.Info("diff rule finding", attributes...)
}

// renderDiffRuleFindings formats the blocking findings as a park reason: one
// line per violation, at the position a reader can jump to.
func renderDiffRuleFindings(findings []diffFinding) string {
	var builder strings.Builder
	builder.WriteString("diff rules found blocking violations:\n")
	for _, finding := range findings {
		fmt.Fprintf(&builder, "- %s:%d: %s", finding.Path, finding.Line, finding.Message)
		if finding.Rule != "" {
			fmt.Fprintf(&builder, " (%s)", finding.Rule)
		}
		builder.WriteString("\n")
	}
	return clip(builder.String(), diffRuleDetailBytes)
}

// decodeDiffRules strictly decodes the step's settings and compiles every rule
// up front.
func decodeDiffRules(settings yaml.Node) ([]compiledDiffRule, error) {
	if settings.Kind == 0 {
		return nil, fmt.Errorf("%s requires settings: a step with no rules would pass every change", DiffRulesStepName)
	}
	var decoded diffRulesSettings
	if err := decodeSettingsStrict(settings, &decoded); err != nil {
		return nil, fmt.Errorf("%s settings: %w", DiffRulesStepName, err)
	}
	if len(decoded.Rules) == 0 {
		return nil, fmt.Errorf("%s: at least one rule is required", DiffRulesStepName)
	}

	rules := make([]compiledDiffRule, 0, len(decoded.Rules))
	for i, rule := range decoded.Rules {
		compiled, err := compileDiffRule(rule)
		if err != nil {
			return nil, fmt.Errorf("%s rule %d: %w", DiffRulesStepName, i+1, err)
		}
		rules = append(rules, compiled)
	}
	return rules, nil
}

// compileDiffRule turns one operator rule into the form the stage matches with,
// compiling its expressions here rather than at the first line of a run: a rule
// that cannot be applied is refused where the definition is validated.
func compileDiffRule(rule DiffRule) (compiledDiffRule, error) {
	compiled := compiledDiffRule{id: rule.ID, level: rule.Level, message: rule.Message}

	switch rule.Level {
	case DiffRuleLevelError, DiffRuleLevelWarn:
	case "":
		return compiledDiffRule{}, errors.New("level is required (error or warn)")
	default:
		return compiledDiffRule{}, fmt.Errorf("unknown level %q, want %q or %q", rule.Level, DiffRuleLevelError, DiffRuleLevelWarn)
	}
	if rule.Message == "" {
		return compiledDiffRule{}, errors.New("message is required")
	}
	if rule.Pattern == "" {
		return compiledDiffRule{}, errors.New("pattern is required")
	}
	pattern, err := regexp.Compile(rule.Pattern)
	if err != nil {
		return compiledDiffRule{}, fmt.Errorf("pattern: %w", err)
	}
	compiled.pattern = pattern

	if rule.Path != "" {
		path, err := regexp.Compile(rule.Path)
		if err != nil {
			return compiledDiffRule{}, fmt.Errorf("path: %w", err)
		}
		compiled.path = path
	}
	if rule.ExcludePath != "" {
		excludePath, err := regexp.Compile(rule.ExcludePath)
		if err != nil {
			return compiledDiffRule{}, fmt.Errorf("exclude_path: %w", err)
		}
		compiled.excludePath = excludePath
	}
	return compiled, nil
}

// decodeSettingsStrict decodes a settings node the way ParseDefinition's own
// decoder reads a definition: unknown fields are errors. yaml.Node.Decode is
// not strict, so the node is re-encoded and decoded with KnownFields rather
// than silently dropping a key this step does not read.
func decodeSettingsStrict(settings yaml.Node, target any) error {
	raw, err := yaml.Marshal(&settings)
	if err != nil {
		return fmt.Errorf("re-encode settings: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	return decoder.Decode(target)
}

// compiledDiffRule is one rule in the form the stage applies it: expressions
// compiled, level checked.
type compiledDiffRule struct {
	id          string
	level       string
	message     string
	pattern     *regexp.Regexp
	path        *regexp.Regexp
	excludePath *regexp.Regexp
}

// appliesTo reports whether this rule covers a changed file's path.
func (r compiledDiffRule) appliesTo(path string) bool {
	if r.path != nil && !r.path.MatchString(path) {
		return false
	}
	return r.excludePath == nil || !r.excludePath.MatchString(path)
}

// diffFinding is one rule violation, at the position the change introduced it.
type diffFinding struct {
	Rule    string
	Level   string
	Message string
	Path    string
	Line    int
}

// matchDiffRules applies every rule to every added line. Rules are not
// short-circuited by the first match: two rules can both object to one line,
// and an operator who wrote both wants to read both.
func matchDiffRules(rules []compiledDiffRule, added []diffLine) []diffFinding {
	var findings []diffFinding
	for _, line := range added {
		for _, rule := range rules {
			if !rule.appliesTo(line.Path) || !rule.pattern.MatchString(line.Text) {
				continue
			}
			findings = append(findings, diffFinding{
				Rule:    rule.id,
				Level:   rule.level,
				Message: rule.message,
				Path:    line.Path,
				Line:    line.Line,
			})
		}
	}
	return findings
}

// diffLine is one added line of a unified diff, positioned in the file the
// change produces -- the number an operator can open, not the diff's own
// offset.
type diffLine struct {
	Path string
	Line int
	Text string
}

// addedDiffLines returns the lines a unified diff adds, with their file path
// and new-file line number, using the prreview diff parser.
func addedDiffLines(diff string) []diffLine {
	added := prreview.AddedLines(prreview.ParseDiff(diff))
	lines := make([]diffLine, 0, len(added))
	for _, line := range added {
		lines = append(lines, diffLine{Path: line.Path, Line: line.Line, Text: line.Text})
	}
	return lines
}
