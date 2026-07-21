// Package nats manages the NATS JetStream connection for task distribution
// and agent communication. When configured, the daemon publishes discovered
// issues to NATS subjects and consumes them back via a JetStream pull
// consumer. Agent workers consume stage execution requests and reply via
// core NATS inboxes. When not configured (nil client), the existing SQLite
// ClaimNext flow is used unchanged.
package nats

import (
	"fmt"
	"strings"
)

// Task distribution subjects. Each subject encodes the workflow type so
// future multi-daemon deployments can filter by workflow.
const (
	SubjectTaskBug       = "archie.task.bug"
	SubjectTaskFeature   = "archie.task.feature"
	SubjectTaskDefault   = "archie.task.default"
	SubjectTaskBootstrap = "archie.task.bootstrap"
)

// SubjectForLabels picks the NATS subject based on issue labels, mirroring
// the label-to-workflow mapping in workflow.Route.
//
//	"bug"       -> archie.task.bug
//	"feature"   -> archie.task.feature
//	"bootstrap" -> archie.task.bootstrap
//	default     -> archie.task.default
func SubjectForLabels(labels []string) string {
	for _, l := range labels {
		switch strings.TrimSpace(l) {
		case "bug":
			return SubjectTaskBug
		case "feature":
			return SubjectTaskFeature
		case "bootstrap":
			return SubjectTaskBootstrap
		}
	}
	return SubjectTaskDefault
}

// Agent communication subjects.
const (
	// SubjectAgentWildcard matches all agent stage execution requests.
	SubjectAgentWildcard = "archie.agent.>"
)

// SubjectForAgentRequest returns the JetStream subject for a stage request.
func SubjectForAgentRequest(taskID int64, stage string) string {
	return fmt.Sprintf("archie.agent.%d.%s", taskID, stage)
}

// ReplyHeader is the custom NATS header carrying the daemon's reply inbox.
// JetStream's PublishMsg consumes the Reply field internally for PubAck, so
// the reply address is carried via this header instead.
const ReplyHeader = "X-Archie-Reply"
