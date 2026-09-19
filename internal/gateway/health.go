package gateway

import (
	"fmt"
	"strings"
	"time"
)

// This file is /status' health contract: the facts the daemon reports about
// its own well-being, and the rules for rendering them. It is deliberately
// separate from the work surface (/tasks, and the identity/stage/age those
// summaries carry): /status answers "is archie well", /tasks answers "what is
// archie doing", and neither repeats the other.
//
// Every section below is optional, and a nil section means "this process has
// no truthful source for that fact". The renderer omits it. It must never
// print a zero, a dash or a probe result in its place: an operator cannot
// tell an invented "0 running" from a measured one, and a probe reports
// whether a fresh connection works rather than whether the daemon's own
// connections are alive.

// BrokerHealth is the state of this process's own task-broker connection.
type BrokerHealth struct {
	// Connected is read from the connection the process already holds,
	// never from a fresh dial (see BrokerHealth's package note above).
	Connected bool
}

// ContainerHealth is the managed worker pool's occupancy. Capacity is the
// configured concurrency cap; zero means unlimited, so it is not a ratio.
type ContainerHealth struct {
	Active int
	Cap    int
}

// ChatModelOutcome is the last-known result of one chat-model call. Err is
// empty when that call succeeded.
type ChatModelOutcome struct {
	// Model is the model reference the call used, so a failure that
	// happened before a /model switch is not read as the new model's.
	Model string
	At    time.Time
	Err   string
}

// ChatModelHealth is what this process knows about its chat-model calls: the
// last one that actually happened, or Attempted=false, meaning none has been
// made since this process started. It is never a live probe -- see the file
// note above.
type ChatModelHealth struct {
	Attempted bool
	Outcome   ChatModelOutcome
}

// ChatModelStatus reports the most recent chat-model call this process made,
// and whether any has been made at all. The daemon records outcomes at the
// one place every chat-model call passes through; nothing here dials a
// provider.
type ChatModelStatus interface {
	LastChatModelOutcome() (ChatModelOutcome, bool)
}

// ChannelHealth is one channel adapter's lifecycle state, as the channel
// manager reports it. Detail carries the failure reason when the adapter
// itself recorded one.
type ChannelHealth struct {
	Name   string
	State  string
	Detail string
}

// HealthReport is a point-in-time snapshot of the daemon's health. Every
// section is optional; see the file note above for what nil means.
type HealthReport struct {
	Broker     *BrokerHealth
	Containers *ContainerHealth
	ChatModel  *ChatModelHealth
	Channels   []ChannelHealth
	// LastPoll is when the most recent forge poll pass began. A nil
	// LastPoll means this process does not poll at all (the standalone
	// Gateway has no poll loop); a non-nil zero time means it does poll
	// but no pass has started yet.
	LastPoll *time.Time
}

// HealthSource reports daemon health for /status. The composition root
// supplies whichever facts the process actually owns -- the daemon's poll
// loop and container pool live in the daemon process, not in the standalone
// Gateway -- and leaves the rest nil.
type HealthSource interface {
	Health() HealthReport
}

// maxChannelDetail caps one channel's detail as it appears in /status. The
// detail is an error string from an adapter and can be arbitrarily long; it
// is also already at its source (the adapter's log and the readiness
// endpoint), so truncating here costs nothing.
const maxChannelDetail = 200

// formatHealth renders the health section of /status, one "Key: value" line
// per reported fact, in a fixed order. It returns "" when nothing was
// reported, so an unwired source leaves the reply exactly as it was.
func formatHealth(report HealthReport, now time.Time) string {
	lines := make([]string, 0, 5)
	if b := report.Broker; b != nil {
		lines = append(lines, "Broker: "+connectivity(b.Connected))
	}
	if c := report.Containers; c != nil {
		lines = append(lines, "Containers: "+containerOccupancy(*c))
	}
	if len(report.Channels) > 0 {
		lines = append(lines, "Channels: "+formatChannels(report.Channels))
	}
	if c := report.ChatModel; c != nil {
		lines = append(lines, "Chat model: "+formatChatModel(*c, now))
	}
	if p := report.LastPoll; p != nil {
		lines = append(lines, "Last poll: "+lastPollAge(*p, now))
	}
	return strings.Join(lines, "\n")
}

func connectivity(connected bool) string {
	if connected {
		return "connected"
	}
	return "disconnected"
}

// containerOccupancy renders active containers against the configured cap.
// An unlimited pool (cap <= 0) renders the count alone: "1/0" would read as
// "at capacity" when it means the opposite.
func containerOccupancy(c ContainerHealth) string {
	if c.Cap <= 0 {
		return fmt.Sprintf("%d active", c.Active)
	}
	return fmt.Sprintf("%d/%d active", c.Active, c.Cap)
}

func formatChannels(channels []ChannelHealth) string {
	parts := make([]string, 0, len(channels))
	for _, c := range channels {
		part := strings.TrimSpace(c.Name + " " + c.State)
		if detail := channelDetail(c.Detail); detail != "" {
			part += " (" + detail + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " · ")
}

// channelDetail collapses an adapter's error detail onto one line, so a
// multi-line error cannot break the chat reply's layout, and bounds its
// length. A failed adapter is the case where an operator most needs the
// reason, so this trims rather than drops it.
func channelDetail(detail string) string {
	flattened := strings.Join(strings.Fields(detail), " ")
	runes := []rune(flattened)
	if len(runes) <= maxChannelDetail {
		return flattened
	}
	return string(runes[:maxChannelDetail]) + "…"
}

func formatChatModel(health ChatModelHealth, now time.Time) string {
	if !health.Attempted {
		// The decision behind this line: /status never probes the provider.
		// Saying so plainly beats a guess, and beats omitting the line, which
		// would leave an operator unable to tell "no call yet" from "the
		// daemon cannot see the provider at all".
		return "no calls attempted yet"
	}
	outcome := health.Outcome
	model := ""
	if outcome.Model != "" {
		model = fmt.Sprintf(" (%s)", outcome.Model)
	}
	age := relativeAge(outcome.At, now)
	switch {
	case outcome.Err == "":
		return fmt.Sprintf("ok %s%s", age, model)
	case age == "":
		return fmt.Sprintf("failed%s: %s", model, outcome.Err)
	default:
		return fmt.Sprintf("failed %s%s: %s", age, model, outcome.Err)
	}
}

// lastPollAge renders when polling last began. The zero time is a real
// answer, not a missing one: a daemon that has not completed a first pass
// yet has not polled, and saying "not yet" keeps that distinct from a stale
// timestamp, which means the poller has stopped or is wedged.
func lastPollAge(at, now time.Time) string {
	if at.IsZero() {
		return "not yet"
	}
	return relativeAge(at, now)
}
