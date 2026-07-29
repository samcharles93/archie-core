package nats

import (
	"fmt"
	"strings"
)

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

// labelSubjects maps an issue label to its task subject. Declared once so the
// mapping is data rather than a switch, and so SubjectForLabels and any future
// reverse lookup cannot drift apart.
var labelSubjects = map[string]string{
	"bug":       SubjectTaskBug,
	"feature":   SubjectTaskFeature,
	"bootstrap": SubjectTaskBootstrap,
}

// SubjectForLabels picks the task subject for a set of issue labels, mirroring
// the label-to-workflow mapping in workflow.Route. The first recognised label
// wins; unrecognised or empty label sets route to SubjectTaskDefault.
func SubjectForLabels(labels []string) string {
	for _, label := range labels {
		if subject, ok := labelSubjects[strings.TrimSpace(label)]; ok {
			return subject
		}
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
