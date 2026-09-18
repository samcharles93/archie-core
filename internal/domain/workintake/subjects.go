package workintake

import (
	"errors"
	"fmt"
	"strings"
)

// Task subjects. Each encodes the routing kind so a multi-daemon deployment
// can filter by the work it wants.
const (
	SubjectTaskBug       = "archie.task.bug"
	SubjectTaskFeature   = "archie.task.feature"
	SubjectTaskBootstrap = "archie.task.bootstrap"
	SubjectTaskDefault   = "archie.task.default"

	// SubjectTaskWildcard matches every task subject.
	SubjectTaskWildcard = "archie.task.>"
)

// SubjectReactionWildcard matches every reaction subject under archie.reaction.
// Reactions are producer-only fan-out events
// (docs/prds/event-sources-and-reactions.md) delivered on a separate
// LimitsPolicy stream, so every registered consumer sees every matching event
// -- unlike the work-queue ARCHIE_TASKS stream, where one consumer claims each
// message.
const SubjectReactionWildcard = "archie.reaction.>"

// ErrUnknownKind reports a kind with no subject. Routing it to the default
// queue instead would silently misdeliver work.
var ErrUnknownKind = errors.New("workintake: unknown task kind")

// Kind is the routing category an issue's labels select.
//
// It is the single axis both the workflow registry and the task subjects
// route on. The label table was once written twice -- a switch in
// workflow.Route and a subject lookup in the NATS package -- so adding a
// label meant remembering both, and the transport had to know what a forge
// label was.
type Kind string

const (
	KindBug       Kind = "bug"
	KindFeature   Kind = "feature"
	KindBootstrap Kind = "bootstrap"

	// KindDefault routes issues carrying no recognised label.
	KindDefault Kind = "default"
)

// labelKinds maps a forge issue label to its routing kind. This is the one
// place the label vocabulary is defined.
var labelKinds = map[string]Kind{
	"bug":       KindBug,
	"feature":   KindFeature,
	"bootstrap": KindBootstrap,
}

// kindSubjects is the closed set of routable kinds.
var kindSubjects = map[Kind]string{
	KindBug:       SubjectTaskBug,
	KindFeature:   SubjectTaskFeature,
	KindBootstrap: SubjectTaskBootstrap,
	KindDefault:   SubjectTaskDefault,
}

// Validate reports whether the kind is routable. The zero kind is accepted
// and means KindDefault, so a caller that does not classify still publishes.
func (k Kind) Validate() error {
	if k == "" {
		return nil
	}
	if _, ok := kindSubjects[k]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownKind, string(k))
	}
	return nil
}

// SubjectForKind renders a kind as its task subject. The zero and any
// unrecognised kind render as SubjectTaskDefault so a message is never
// addressed to an unroutable subject; publishers validate first.
func SubjectForKind(kind Kind) string {
	if subject, ok := kindSubjects[kind]; ok {
		return subject
	}
	return SubjectTaskDefault
}

// KindForLabels returns the routing kind for a set of issue labels. The first
// recognised label wins; an empty or unrecognised set is KindDefault.
func KindForLabels(labels []string) Kind {
	for _, kind := range KindsForLabels(labels) {
		return kind
	}
	return KindDefault
}

// KindsForLabels returns every recognised kind in label order.
//
// Workflow routing needs the full ordered set rather than just the first:
// when a task is labelled "bug,feature" and no "tdd" workflow is registered,
// it must still fall through to "feasibility" rather than to the default.
func KindsForLabels(labels []string) []Kind {
	var kinds []Kind
	for _, label := range labels {
		if kind, ok := labelKinds[labelValue(label)]; ok {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// labelValue reduces a forge label to the value labelKinds is keyed on.
//
// Forges namespace labels by convention ("type::feature", "priority::medium")
// and the namespace is the issue tracker's own taxonomy, not part of the
// routing vocabulary, so the value after the last "::" is what identifies a
// kind. Matching the bare string alone meant a repo that namespaces its
// labels -- which this one does -- never matched any kind, and every task
// fell through kind routing to the content-aware fallback in Route.
//
// The namespace itself is deliberately not checked: a closed three-value
// vocabulary (bug, feature, bootstrap) is specific enough that a label ending
// in one of them means that kind whatever the tracker files it under.
func labelValue(label string) string {
	trimmed := strings.TrimSpace(label)
	if i := strings.LastIndex(trimmed, "::"); i >= 0 {
		return strings.TrimSpace(trimmed[i+len("::"):])
	}
	return trimmed
}
