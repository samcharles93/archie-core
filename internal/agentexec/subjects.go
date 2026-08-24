package agentexec

import (
	"strconv"
	"strings"
)

// Agent observability subjects.
//
// These belong to this package because it defines what an agent request and
// response mean. They previously sat in the NATS package, which made the
// transport the owner of Archie's agent protocol; see
// docs/architecture/dependencies-and-contracts.md -- message schemas and the
// subjects addressing them stay with the domain that defines them.
const (
	// SubjectSystemWildcard matches every task's system subject, for a
	// daemon subscribing to all of them at once with a single core NATS
	// subscription rather than one per running task.
	SubjectSystemWildcard = subjectAgentPrefix + "*.system"

	// SubjectEventsWildcard matches every task's events subject, for a
	// daemon subscribing to all of them at once with a single core NATS
	// subscription rather than one per running task.
	SubjectEventsWildcard = subjectAgentPrefix + "*.events"

	subjectAgentPrefix  = "archie.agent."
	subjectSystemSuffix = ".system"
	subjectEventsSuffix = ".events"
)

// SubjectForSystem returns the subject for internal agent messages such as
// log dumps, health, and PII warnings. The daemon reads these for
// observability and never forwards them.
func SubjectForSystem(taskID int64) string {
	return subjectForTask(taskID, "system")
}

// SubjectForEvents returns the subject for a task's observability event
// stream (stage progress, outcome, parking) shipped from an archie-agent
// worker back to the daemon that owns the events table and the dashboard
// timeline built from it.
func SubjectForEvents(taskID int64) string {
	return subjectForTask(taskID, "events")
}

// subjectForTask builds a per-task agent subject.
func subjectForTask(taskID int64, kind string) string {
	return subjectAgentPrefix + strconv.FormatInt(taskID, 10) + "." + kind
}

// TaskIDFromSystemSubject extracts the task ID from a subject produced by
// SubjectForSystem, for a daemon demuxing a SubjectSystemWildcard
// subscription. Rejects (0, false) for anything that isn't exactly that
// shape, rather than parsing a partial or wrong-kind subject into a
// misleading task ID.
func TaskIDFromSystemSubject(subject string) (int64, bool) {
	if !strings.HasPrefix(subject, subjectAgentPrefix) || !strings.HasSuffix(subject, subjectSystemSuffix) {
		return 0, false
	}
	middle := strings.TrimSuffix(strings.TrimPrefix(subject, subjectAgentPrefix), subjectSystemSuffix)
	id, err := strconv.ParseInt(middle, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// TaskIDFromEventsSubject extracts the task ID from a subject produced by
// SubjectForEvents, for a daemon demuxing a SubjectEventsWildcard
// subscription. Rejects (0, false) for anything that isn't exactly that
// shape, rather than parsing a partial or wrong-kind subject into a
// misleading task ID.
func TaskIDFromEventsSubject(subject string) (int64, bool) {
	if !strings.HasPrefix(subject, subjectAgentPrefix) || !strings.HasSuffix(subject, subjectEventsSuffix) {
		return 0, false
	}
	middle := strings.TrimSuffix(strings.TrimPrefix(subject, subjectAgentPrefix), subjectEventsSuffix)
	id, err := strconv.ParseInt(middle, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
