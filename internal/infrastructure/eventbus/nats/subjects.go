package nats

import "fmt"

// Task distribution subjects. Each encodes the workflow type so a multi-daemon
// deployment can filter by workflow.
const (
	SubjectTaskBug       = "archie.task.bug"
	SubjectTaskFeature   = "archie.task.feature"
	SubjectTaskBootstrap = "archie.task.bootstrap"
	SubjectTaskDefault   = "archie.task.default"

	// SubjectTaskWildcard matches every task subject.
	SubjectTaskWildcard = "archie.task.>"

	// SubjectAgentWildcard matches every agent stage execution subject.
	SubjectAgentWildcard = "archie.agent.>"
)

// ReplyHeader carries the requester's reply inbox. JetStream consumes a
// message's Reply field for its own PubAck, so the address travels as a header.
const ReplyHeader = "X-Archie-Reply"

// TaskKind is the routing token in a task subject.
//
// The bus deliberately does not derive this itself. It previously inspected
// forge issue labels to choose a subject, which put a forge vocabulary
// ("bug", "feature") inside infrastructure and duplicated the label table
// already owned by the workflow package. The publisher now decides the kind
// and the bus only renders it as a subject.
type TaskKind string

const (
	TaskKindBug       TaskKind = "bug"
	TaskKindFeature   TaskKind = "feature"
	TaskKindBootstrap TaskKind = "bootstrap"

	// TaskKindDefault carries tasks that match no more specific kind.
	TaskKindDefault TaskKind = "default"
)

// taskSubjects is the closed set of routable kinds. A kind absent from this
// map has no subject, so publishing it is a caller error rather than a
// silent delivery to the default queue.
var taskSubjects = map[TaskKind]string{
	TaskKindBug:       SubjectTaskBug,
	TaskKindFeature:   SubjectTaskFeature,
	TaskKindBootstrap: SubjectTaskBootstrap,
	TaskKindDefault:   SubjectTaskDefault,
}

// Validate reports whether the kind is routable. The zero kind is accepted
// and means TaskKindDefault, so callers that do not classify still publish.
func (k TaskKind) Validate() error {
	if k == "" {
		return nil
	}
	if _, ok := taskSubjects[k]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownTaskKind, string(k))
	}
	return nil
}

// SubjectForKind renders a kind as its task subject. The zero and any
// unrecognised kind render as SubjectTaskDefault so a message is never
// published to an unroutable subject; PublishTask rejects unknown kinds
// before reaching here.
func SubjectForKind(kind TaskKind) string {
	if subject, ok := taskSubjects[kind]; ok {
		return subject
	}
	return SubjectTaskDefault
}

// SubjectForAgentRequest returns the subject carrying stage execution requests
// for a task. The stage travels in the payload, not the subject: one subject
// per task with sequential stage messages.
func SubjectForAgentRequest(taskID int64) string {
	return fmt.Sprintf("archie.agent.%d.request", taskID)
}

// SubjectForAgentResponse returns the subject for agent output destined for
// human channels. The daemon reviews these before forwarding to issue
// comments, labels, or pull requests.
func SubjectForAgentResponse(taskID int64) string {
	return fmt.Sprintf("archie.agent.%d.response", taskID)
}

// SubjectForAgentSystem returns the subject for internal agent messages such
// as log dumps, health, and PII warnings. The daemon reads these for
// observability and never forwards them.
func SubjectForAgentSystem(taskID int64) string {
	return fmt.Sprintf("archie.agent.%d.system", taskID)
}
