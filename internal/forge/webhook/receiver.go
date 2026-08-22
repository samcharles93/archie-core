// Package webhook implements the GitHub forge webhook receiver: an HTTP
// endpoint that verifies a GitHub webhook's HMAC signature, decodes the
// "issues" event, and publishes a matched issue as a workintake.TaskEnvelope.
//
// It is forge intake, distinct from internal/channels/webhook (the chat
// channel): that package routes inbound text through the gateway for LLM
// processing; this one decodes a GitHub payload into the same TaskEnvelope
// the poller produces and hands it to the same publish path, so a labelled or
// assigned issue becomes work the moment it happens rather than up to
// poll_interval later (see docs/prds/event-sources-and-reactions.md).
//
// GitHub's webhook must be configured with "Content type: application/json"
// (the UI default). The alternate application/x-www-form-urlencoded delivery
// is not decoded here and is rejected as a bad payload.
//
// Idempotency is not this package's job: it decodes into the same
// TaskEnvelope and calls the same publish path the poller uses, so
// PublishUnique's dedup (keyed on TaskEnvelope.IdempotencyKey) covers both
// sources for free -- see archie-core-7d5u.5. What this package does own is
// making a wedged or non-delivering receiver observable: a GET to the
// listen address (GitHub only ever POSTs) returns Status, tracking
// authenticated deliveries and successful publishes separately so an
// operator can tell "receiver is up but nothing is arriving" apart from
// "repo has no issue activity."
//
// The GET/Status route is deliberately unauthenticated, on the same
// internet-facing listener GitHub POSTs signed payloads to: it reveals
// counters and timestamps only, never repo data, issue content, or the
// webhook secret, so treating it as a public liveness probe (the same
// tradeoff an unauthenticated /healthz makes) is an acceptable exchange for
// not requiring a second credential just to check if the receiver is alive.
package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/go-github/v78/github"

	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/webhookguard"
)

// maxBodyBytes bounds a webhook payload before it is read. GitHub webhook
// bodies are small; the cap exists so a hostile sender cannot exhaust memory.
const maxBodyBytes = 1 << 20 // 1 MiB

// PublishFunc delivers a decoded, dispatch-matched task envelope to the
// daemon's publish path. The composition root wires it to
// (*daemon.Daemon).PublishTask, the same enqueue path the poller uses.
type PublishFunc func(ctx context.Context, task workintake.TaskEnvelope) error

// Receiver is the HTTP handler for forge webhooks. It verifies the signature,
// decodes the event, applies the shared dispatch predicate, and publishes
// matched issues.
type Receiver struct {
	secret  string
	trigger string
	label   string
	botUser string
	publish PublishFunc
	log     *slog.Logger

	mu             sync.Mutex
	startedAt      time.Time
	lastReceivedAt time.Time
	deliveries     uint64
	publishes      uint64
}

// New returns an unstarted Receiver.
func New(secret, trigger, label, botUser string, publish PublishFunc, log *slog.Logger) *Receiver {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Receiver{
		secret:    secret,
		trigger:   trigger,
		label:     label,
		botUser:   botUser,
		publish:   publish,
		log:       log.With("component", "forge-webhook"),
		startedAt: time.Now(),
	}
}

// Status reports the receiver's delivery activity so an operator can tell a
// wedged or non-delivering receiver apart from a repo with no issue
// activity (archie-core-7d5u.5): Deliveries counts every authenticated,
// parseable webhook -- proof GitHub reached this process at all -- while
// Publishes counts only what the dispatch predicate turned into work.
// LastReceivedAt is nil until the first authenticated delivery arrives.
type Status struct {
	StartedAt      time.Time  `json:"started_at"`
	LastReceivedAt *time.Time `json:"last_received_at"`
	Deliveries     uint64     `json:"deliveries"`
	Publishes      uint64     `json:"publishes"`
}

// Status returns a snapshot of the receiver's activity counters.
func (r *Receiver) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Status{
		StartedAt:  r.startedAt,
		Deliveries: r.deliveries,
		Publishes:  r.publishes,
	}
	if !r.lastReceivedAt.IsZero() {
		last := r.lastReceivedAt
		s.LastReceivedAt = &last
	}
	return s
}

// recordDelivery marks one authenticated, parseable webhook as received.
// Called only after HMAC verification and payload parsing succeed, so an
// unauthenticated request (anyone spamming the endpoint with a bad
// signature) can never inflate the "receiver is alive" signal.
func (r *Receiver) recordDelivery() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deliveries++
	r.lastReceivedAt = time.Now()
}

// recordPublish marks one delivery as having produced a task.
func (r *Receiver) recordPublish() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.publishes++
}

// ServeHTTP handles one forge webhook delivery. GitHub only ever POSTs here,
// so any GET is answered with the receiver's Status instead -- an operator
// (or a monitor) can curl the same address the webhook is configured against
// to check liveness, with no separate health-check surface to wire up.
func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(r.Status())
		return
	}
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(req.Body, maxBodyBytes))
	if err != nil {
		r.log.Error("read body", "err", err)
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	// HMAC verification. An invalid or missing signature is rejected and
	// logged, never silently accepted.
	sig := req.Header.Get("X-Hub-Signature-256")
	if !webhookguard.VerifyHMAC(body, sig, r.secret) {
		r.log.Warn("invalid signature", "remote", req.RemoteAddr)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	event, err := github.ParseWebHook(req.Header.Get("X-GitHub-Event"), body)
	if err != nil {
		r.log.Warn("parse webhook", "err", err)
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	// Authenticated and parseable: this is a genuine GitHub delivery,
	// independent of whether it turns out to be dispatch-eligible.
	r.recordDelivery()

	issueEvent, ok := event.(*github.IssuesEvent)
	if !ok {
		// Not an issue event (ping, pull_request, etc.). Acknowledge and
		// ignore -- this receiver only turns issue activity into work.
		w.WriteHeader(http.StatusAccepted)
		return
	}

	task, ok := r.taskFromEvent(issueEvent)
	if !ok {
		// Delivered but not eligible work (PR, closed, unrelated action,
		// dispatch mismatch). Acknowledge without publishing.
		w.WriteHeader(http.StatusAccepted)
		return
	}

	if err := r.publish(req.Context(), task); err != nil {
		r.log.Error("publish task", "task", task.Ref(), "err", err)
		http.Error(w, "publish failed", http.StatusInternalServerError)
		return
	}
	r.recordPublish()
	r.log.Info("published", "task", task.Ref())
	w.WriteHeader(http.StatusAccepted)
}

// taskFromEvent decodes a GitHub issues event into a task envelope, or
// reports (zero, false) when the event is not eligible work.
func (r *Receiver) taskFromEvent(event *github.IssuesEvent) (workintake.TaskEnvelope, bool) {
	issue := event.GetIssue()
	if issue == nil || issue.IsPullRequest() {
		return workintake.TaskEnvelope{}, false
	}
	// Only events that could newly make an issue eligible. unlabeled,
	// unassigned, closed, and edited never queue work.
	switch event.GetAction() {
	case "opened", "reopened", "labeled", "assigned":
	default:
		return workintake.TaskEnvelope{}, false
	}
	if issue.GetState() != "open" {
		return workintake.TaskEnvelope{}, false
	}

	labels := labelNames(issue.Labels)
	assignees := assigneeLogins(issue.Assignees, issue.Assignee, event.GetAssignee())
	if !workintake.MatchesDispatch(r.trigger, r.label, r.botUser, labels, assignees) {
		return workintake.TaskEnvelope{}, false
	}

	repo := event.GetRepo()
	if repo == nil || repo.GetOwner().GetLogin() == "" || repo.GetName() == "" {
		r.log.Warn("event missing repository", "action", event.GetAction())
		return workintake.TaskEnvelope{}, false
	}

	return workintake.TaskEnvelope{
		Owner:  repo.GetOwner().GetLogin(),
		Repo:   repo.GetName(),
		Number: issue.GetNumber(),
		Title:  issue.GetTitle(),
		Body:   issue.GetBody(),
		Labels: labels,
		Kind:   workintake.KindForLabels(labels),
	}, true
}

func labelNames(labels []*github.Label) []string {
	var out []string
	for _, l := range labels {
		if name := l.GetName(); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func assigneeLogins(assignees []*github.User, extra ...*github.User) []string {
	seen := map[string]bool{}
	var out []string
	add := func(u *github.User) {
		if u == nil {
			return
		}
		login := u.GetLogin()
		if login == "" || seen[login] {
			return
		}
		seen[login] = true
		out = append(out, login)
	}
	for _, a := range assignees {
		add(a)
	}
	for _, a := range extra {
		add(a)
	}
	return out
}
