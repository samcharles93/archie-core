package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Class is a finding's conformance classification. Only Undeclared and Stale
// are findings; the other two are healthy states that are still reported so a
// reader can see the surface was actually walked rather than skipped.
type Class string

const (
	// Consumed: a non-test client call site exercises the subject.
	Consumed Class = "CONSUMED"
	// DeclaredUnconsumed: named in the declaration allowlist with a reason and
	// a tracker id. This is a status, not a defect -- tracked work whose only
	// property is that its completion state was not visible anywhere.
	DeclaredUnconsumed Class = "DECLARED-UNCONSUMED"
	// Undeclared: nobody reaches the subject and nobody declared it. The finding.
	Undeclared Class = "UNDECLARED-UNCONSUMED"
	// Stale: a declaration naming a subject that is now consumed, or that no
	// longer exists on the surface. A stale allowlist is how a gate quietly
	// stops checking anything, so it fails like an undeclared entry.
	Stale Class = "STALE-DECLARATION"
)

// Finding is one surface subject's conformance state. The shape is consumed by
// the text and JSON renderers and by the tests, so it is stable.
type Finding struct {
	Surface  string `json:"surface"`
	Subject  string `json:"subject"`
	Class    Class  `json:"class"`
	Evidence string `json:"evidence,omitempty"`
	Tracker  string `json:"tracker,omitempty"`
}

// Declaration is one allowlisted unconsumed subject.
type Declaration struct {
	// Reason states the consequence, not "TODO". This file is the only place
	// the debt is legible.
	Reason string `json:"reason"`
	// Tracker is the bead id or issue URL that owns the work.
	Tracker string `json:"tracker,omitempty"`
}

// Declarations is the allowlist, keyed by surface name then subject. It must
// match reality exactly: a missing entry is an undeclared finding and an entry
// that is no longer true is a stale one.
type Declarations struct {
	// Surfaces maps a surface name (e.g. "proto/state/v1") to its declared
	// unconsumed subjects.
	Surfaces map[string]map[string]Declaration `json:"surfaces"`
	// BlindSpots names the checks that cannot be exact, with the reason. These
	// are printed as-is and are never counted as findings -- the reachaudit
	// rule that a static-analysis blind spot is labelled, not presented as dead.
	BlindSpots []BlindSpot `json:"blindSpots,omitempty"`
}

// BlindSpot is a check limitation reported rather than silently applied.
type BlindSpot struct {
	Surface string `json:"surface"`
	Reason  string `json:"reason"`
}

// Subject is one addressable element of a surface: an RPC, an HTTP route, a
// generated artifact.
type Subject struct {
	// Name is the stable identity, e.g. "StateStoreService/TokensByDay" or
	// "GET /api/summary".
	Name string
	// Evidence is where the subject is declared, as file:line where known.
	Evidence string
	// Consumed reports whether a client reaches it.
	Consumed bool
	// Consumer is the consuming call site, for the report.
	Consumer string
}

// Surface is one declared contract surface and the subjects it exposes.
type Surface interface {
	// Name is the surface's stable identifier, matching the declaration file.
	Name() string
	// Subjects enumerates the surface's declared subjects and their
	// consumption state.
	Subjects() ([]Subject, error)
}

// Audit walks every surface and classifies each subject against the
// declaration allowlist.
type Audit struct {
	root         string
	declPath     string
	declarations Declarations
	surfaces     []Surface
}

// New builds an audit over the repository at root, reading the declaration
// allowlist from declPath (relative to root).
func New(root, declPath string) (*Audit, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root: %w", err)
	}
	absDecl := declPath
	if !filepath.IsAbs(absDecl) {
		absDecl = filepath.Join(absRoot, declPath)
	}
	declarations, err := loadDeclarations(absDecl)
	if err != nil {
		return nil, err
	}
	return &Audit{
		root:         absRoot,
		declPath:     absDecl,
		declarations: declarations,
		surfaces: []Surface{
			newStateStoreSurface(absRoot),
			newGatewayChatSurface(absRoot),
			newWebUISurface(absRoot),
		},
	}, nil
}

// parseDeclarations decodes the allowlist from r. Decoding is separate from
// reading the file so its rules can be exercised without authoring a file.
func parseDeclarations(r io.Reader) (Declarations, error) {
	var declarations Declarations
	if err := json.NewDecoder(r).Decode(&declarations); err != nil {
		return Declarations{}, fmt.Errorf("parse declarations: %w", err)
	}
	if declarations.Surfaces == nil {
		declarations.Surfaces = map[string]map[string]Declaration{}
	}
	return declarations, nil
}

// loadDeclarations reads the allowlist. A missing file is not an error: an
// empty allowlist is the correct starting state and makes every unconsumed
// subject an undeclared finding, which is the honest first report.
func loadDeclarations(path string) (Declarations, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return Declarations{Surfaces: map[string]map[string]Declaration{}}, nil
	}
	if err != nil {
		return Declarations{}, fmt.Errorf("read declarations: %w", err)
	}
	defer f.Close()
	return parseDeclarations(f)
}

// Run walks every surface and returns findings sorted by surface then subject.
func (a *Audit) Run() ([]Finding, error) {
	var findings []Finding
	for _, surface := range a.surfaces {
		subjects, err := surface.Subjects()
		if err != nil {
			return nil, fmt.Errorf("surface %s: %w", surface.Name(), err)
		}
		findings = append(findings, classify(surface.Name(), subjects, a.declarations)...)
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Surface != findings[j].Surface {
			return findings[i].Surface < findings[j].Surface
		}
		return findings[i].Subject < findings[j].Subject
	})
	return findings, nil
}

// classify applies the three-state rule, then the staleness rule in the
// opposite direction: every declaration must name a subject that still exists
// and is still unconsumed.
func classify(surface string, subjects []Subject, declarations Declarations) []Finding {
	declared := declarations.Surfaces[surface]
	findings := make([]Finding, 0, len(subjects)+len(declared))
	seen := make(map[string]bool, len(subjects))

	for _, subject := range subjects {
		seen[subject.Name] = true
		declaration, isDeclared := declared[subject.Name]
		switch {
		case subject.Consumed && isDeclared:
			// The declaration outlived its subject's unconsumed state.
			findings = append(findings, Finding{
				Surface:  surface,
				Subject:  subject.Name,
				Class:    Stale,
				Evidence: fmt.Sprintf("declared unconsumed but consumed by %s", subject.Consumer),
				Tracker:  declaration.Tracker,
			})
		case subject.Consumed:
			findings = append(findings, Finding{
				Surface:  surface,
				Subject:  subject.Name,
				Class:    Consumed,
				Evidence: subject.Consumer,
			})
		case isDeclared:
			findings = append(findings, Finding{
				Surface:  surface,
				Subject:  subject.Name,
				Class:    DeclaredUnconsumed,
				Evidence: declaration.Reason,
				Tracker:  declaration.Tracker,
			})
		default:
			findings = append(findings, Finding{
				Surface:  surface,
				Subject:  subject.Name,
				Class:    Undeclared,
				Evidence: subject.Evidence,
			})
		}
	}

	// A declaration for a subject the surface no longer exposes is stale too:
	// the allowlist is now permitting something that does not exist, which is
	// indistinguishable from permitting something nobody checked.
	for name, declaration := range declared {
		if seen[name] {
			continue
		}
		findings = append(findings, Finding{
			Surface:  surface,
			Subject:  name,
			Class:    Stale,
			Evidence: "declared, but the surface no longer exposes this subject",
			Tracker:  declaration.Tracker,
		})
	}
	return findings
}

// UndeclaredCount is the number of findings that should fail a strict run.
func UndeclaredCount(findings []Finding) int {
	n := 0
	for _, finding := range findings {
		if finding.Class == Undeclared || finding.Class == Stale {
			n++
		}
	}
	return n
}

func writeJSON(out io.Writer, findings []Finding) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(findings)
}

func writeText(out io.Writer, findings []Finding) {
	bySurface := map[string][]Finding{}
	var order []string
	for _, finding := range findings {
		if _, ok := bySurface[finding.Surface]; !ok {
			order = append(order, finding.Surface)
		}
		bySurface[finding.Surface] = append(bySurface[finding.Surface], finding)
	}
	sort.Strings(order)
	for _, surface := range order {
		group := bySurface[surface]
		counts := map[Class]int{}
		for _, finding := range group {
			counts[finding.Class]++
		}
		fmt.Fprintf(out, "\n%s  (%s %d, %s %d, %s %d, %s %d)\n",
			surface,
			Consumed, counts[Consumed],
			DeclaredUnconsumed, counts[DeclaredUnconsumed],
			Undeclared, counts[Undeclared],
			Stale, counts[Stale],
		)
		for _, finding := range group {
			if finding.Class == Consumed {
				continue
			}
			line := fmt.Sprintf("  %-22s %s", finding.Class, finding.Subject)
			if finding.Tracker != "" {
				line += "  [" + finding.Tracker + "]"
			}
			fmt.Fprintln(out, line)
			if finding.Evidence != "" {
				fmt.Fprintf(out, "      %s\n", strings.TrimSpace(finding.Evidence))
			}
		}
	}
}
