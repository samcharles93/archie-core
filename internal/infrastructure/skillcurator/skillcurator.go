package skillcurator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/curator"
	"github.com/samcharles93/archie-core/internal/skill"
)

// Name is the identifier this curator registers under.
const Name = "skill"

// DefaultInterval is the check-in cadence used when nothing more specific
// configures one. No config surface for per-curator intervals exists yet
// (this is the first real curator registered); this constant is the
// obvious place a future one would read from.
const DefaultInterval = time.Hour

const (
	// ActionInvalid records a skill whose SKILL.md failed to parse.
	ActionInvalid = "skill.invalid"
	// ActionIncomplete records a skill that parsed but is missing a
	// required frontmatter field.
	ActionIncomplete = "skill.incomplete"
	// ActionNormalized records a skill whose content was rewritten by
	// whitespace normalization.
	ActionNormalized = "skill.normalized"
)

// Curator implements domain/curator.CuratorEngine. See
// docs/prds/skill-curator.md for what a pass does and why: mechanical
// validation and safe whitespace normalization only -- no LLM-authored
// rewrites, no deletion.
type Curator struct {
	interval time.Duration
	host     curator.Registrar
}

// New builds a skill curator that checks in every interval.
func New(interval time.Duration) *Curator {
	return &Curator{interval: interval}
}

func (c *Curator) Name() string    { return Name }
func (c *Curator) Version() string { return "1" }

func (c *Curator) Manifest() curator.Manifest {
	return curator.Manifest{Interval: c.interval, Skills: true}
}

func (c *Curator) Bind(host curator.Registrar) { c.host = host }

func (c *Curator) Start(context.Context) error { return nil }

func (c *Curator) Health(context.Context) curator.Health {
	return curator.Health{Status: curator.HealthHealthy}
}

func (c *Curator) Stop(context.Context) error { return nil }

// Check reports whether there is anything to review. Cheap by
// construction: a directory listing, no model calls, no parsing.
func (c *Curator) Check(ctx context.Context) (bool, error) {
	refs, err := c.host.Skills.List(ctx)
	if err != nil {
		return false, err
	}
	return len(refs) > 0, nil
}

// Pass reviews every skill currently on disk. There is no per-skill
// "already reviewed" state: each pass re-checks everything, which is
// cheap (file reads, no model calls) and errs toward catching a
// regression over missing one. See docs/prds/skill-curator.md for the
// exact classification rules.
func (c *Curator) Pass(ctx context.Context, in curator.PassInput) (curator.PassResult, error) {
	refs, err := c.host.Skills.List(ctx)
	if err != nil {
		return curator.PassResult{}, err
	}

	var actions []curator.Action
	for _, ref := range refs {
		acts, err := c.reviewOne(ctx, ref.Name)
		if err != nil {
			return curator.PassResult{}, fmt.Errorf("skill %q: %w", ref.Name, err)
		}
		actions = append(actions, acts...)
	}
	return curator.PassResult{Actions: actions}, nil
}

// reviewOne classifies a single skill and, for a normalizable one,
// writes the cleaned content back. It never returns more than one
// Action: a parse failure and a missing field can't both be reported for
// the same skill in the same pass (a Frontmatter that failed to parse
// has no fields to check), and a normalization is reported once even
// though it may have fixed multiple lines.
func (c *Curator) reviewOne(ctx context.Context, name string) ([]curator.Action, error) {
	sk, err := c.host.Skills.Read(ctx, name)
	if err != nil {
		return nil, err
	}

	fm, _, fail := skill.Parse([]byte(sk.Content))
	if fail != nil {
		// A parse failure is a per-skill finding to report, not an
		// execution error to propagate: Pass()'s loop aborts the whole
		// batch on a non-nil error, which would stop reviewing every
		// other skill over one bad file (see skillcurator_test.go's
		// "must not abort review of the others").
		return invalidFinding(name, fail)
	}

	if missing := missingField(fm); missing != "" {
		return []curator.Action{{
			Type:   ActionIncomplete,
			Detail: name,
			Reason: "missing " + missing,
		}}, nil
	}

	normalized := normalizeWhitespace(sk.Content)
	if normalized == sk.Content {
		return nil, nil
	}
	if err := c.host.Skills.Write(ctx, curator.Skill{Name: name, Content: normalized}); err != nil {
		return nil, err
	}
	return []curator.Action{{
		Type:   ActionNormalized,
		Detail: name,
		Reason: "trailing whitespace or missing final newline",
	}}, nil
}

// invalidFinding converts a parse error into a per-skill action. Parse
// failures are expected findings, so they must not abort the rest of Pass.
func invalidFinding(name string, parseErr error) ([]curator.Action, error) {
	return []curator.Action{{
		Type:   ActionInvalid,
		Detail: name,
		Reason: parseErr.Error(),
	}}, nil
}

// missingField returns the name of the first required frontmatter field
// fm lacks, or "" if both are present. Order (name before description)
// only affects which single reason is reported when both are missing;
// either is worth flagging on its own.
func missingField(fm *skill.Frontmatter) string {
	if strings.TrimSpace(fm.Name) == "" {
		return "name"
	}
	if strings.TrimSpace(fm.Description) == "" {
		return "description"
	}
	return ""
}

// normalizeWhitespace trims trailing whitespace from every line and
// ensures the content ends in exactly one newline. It never touches
// leading whitespace (meaningful in YAML frontmatter and Markdown list
// nesting) or blank-line structure.
func normalizeWhitespace(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}
