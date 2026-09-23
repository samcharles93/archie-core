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

// ReactionPublishFunc delivers one decoded review reaction to the reaction
// stream (pr-review-remediation.md decision 2's webhook producer). The
// composition root wires it to PublishUnique against the envelope's reaction
// subject, so a webhook delivery and a poll record of the same review dedup
// on the envelope's source-independent key. The reaction path carries no
// dispatch predicate: eligibility is the consumer's owned-PR lookup.
type ReactionPublishFunc func(ctx context.Context, reaction workintake.ReviewCommentEnvelope) error

// reactionRateLimit bounds reaction deliveries per remote source per window
// (decision 6's per-source rate limiting, due now that the receiver carries a
// second event family). It guards spend, not authorization: the owned-task
// guard at the consumer remains the authorization boundary.
const (
	reactionRateLimit     = 60
	reactionRateWindow    = time.Minute
	reactionRateBucketTTL = 5 * time.Minute
)

// Receiver is the HTTP handler for forge webhooks. It verifies the signature,
// decodes the event, applies the shared dispatch predicate, and publishes
// matched issues.
type Receiver struct {
	secret          string
	trigger         string
	label           string
	botUser         string
	publish         PublishFunc
	reactionPublish ReactionPublishFunc
	log             *slog.Logger

	mu                sync.Mutex
	startedAt         time.Time
	lastReceivedAt    time.Time
	deliveries        uint64
	publishes         uint64
	reactionPublishes uint64

	reactionLimiter map[string]*reactionWindow
}

// reactionWindow is one remote's fixed-window rate budget.
type reactionWindow struct {
	start time.Time
	count int
}

// New returns an unstarted Receiver.
func New(secret, trigger, label, botUser string, publish PublishFunc, reactionPublish ReactionPublishFunc, log *slog.Logger) *Receiver {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Receiver{
		secret:          secret,
		trigger:         trigger,
		label:           label,
		botUser:         botUser,
		publish:         publish,
		reactionPublish: reactionPublish,
		reactionLimiter: map[string]*reactionWindow{},
		log:             log.With("component", "forge-webhook"),
		startedAt:       time.Now(),
	}
}

// Status reports the receiver's delivery activity so an operator can tell a
// wedged or non-delivering receiver apart from a repo with no issue
// activity (archie-core-7d5u.5): Deliveries counts every authenticated,
// parseable webhook -- proof GitHub reached this process at all -- while
// Publishes counts only what the dispatch predicate turned into work.
// LastReceivedAt is nil until the first authenticated delivery arrives.
type Status struct {
	StartedAt         time.Time  `json:"started_at"`
	LastReceivedAt    *time.Time `json:"last_received_at"`
	Deliveries        uint64     `json:"deliveries"`
	Publishes         uint64     `json:"publishes"`
	ReactionPublishes uint64     `json:"reaction_publishes"`
}

// Status returns a snapshot of the receiver's activity counters.
func (r *Receiver) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Status{
		StartedAt:         r.startedAt,
		Deliveries:        r.deliveries,
		Publishes:         r.publishes,
		ReactionPublishes: r.reactionPublishes,
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

// recordReactionPublish marks one delivery as having produced a reaction.
func (r *Receiver) recordReactionPublish() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reactionPublishes++
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

	switch e := event.(type) {
	case *github.IssuesEvent:
		r.serveIssueEvent(w, req, e)
	case *github.PullRequestReviewEvent:
		r.serveReviewEvent(w, req, e)
	case *github.PullRequestReviewCommentEvent:
		r.serveReviewCommentEvent(w, req, e)
	default:
		// Not an event this receiver decodes (ping, push, etc.).
		// Acknowledge and ignore.
		w.WriteHeader(http.StatusAccepted)
	}
}

// serveIssueEvent is the receiver's original contract: an eligible issue
// event becomes a task envelope on the task path.
func (r *Receiver) serveIssueEvent(w http.ResponseWriter, req *http.Request, issueEvent *github.IssuesEvent) {
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

// serveReviewEvent turns a submitted review into a reaction, the webhook
// half of decision 2: the same typed reaction the poller produces, so
// PublishUnique dedups the two sources. Only "submitted" carries a verdict
// or summary to remediate; edited and dismissed restate or withdraw one.
func (r *Receiver) serveReviewEvent(w http.ResponseWriter, req *http.Request, e *github.PullRequestReviewEvent) {
	if e.GetAction() != "submitted" || e.GetReview() == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if !r.allowReactionDelivery(req.RemoteAddr) {
		r.log.Warn("reaction rate limit exceeded", "remote", req.RemoteAddr)
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	if r.reactionPublish == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	review := e.GetReview()
	reaction := workintake.ReviewCommentEnvelope{
		Owner:    e.GetRepo().GetOwner().GetLogin(),
		Repo:     e.GetRepo().GetName(),
		PRNumber: e.GetPullRequest().GetNumber(),
		Kind:     workintake.ReviewReactionReview,
		ReviewID: review.GetID(),
		Author:   review.GetUser().GetLogin(),
		State:    review.GetState(),
		Body:     review.GetBody(),
	}
	r.deliverReaction(w, req, reaction)
}

// serveReviewCommentEvent publishes an inline comment as a reaction. The
// comment carries its parent review's ID, so the consumer can collect it
// into that review's unit (decision 5) rather than starting a round of its
// own.
func (r *Receiver) serveReviewCommentEvent(w http.ResponseWriter, req *http.Request, e *github.PullRequestReviewCommentEvent) {
	if e.GetAction() != "created" || e.GetComment() == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if !r.allowReactionDelivery(req.RemoteAddr) {
		r.log.Warn("reaction rate limit exceeded", "remote", req.RemoteAddr)
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	if r.reactionPublish == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	comment := e.GetComment()
	reaction := workintake.ReviewCommentEnvelope{
		Owner:     e.GetRepo().GetOwner().GetLogin(),
		Repo:      e.GetRepo().GetName(),
		PRNumber:  e.GetPullRequest().GetNumber(),
		Kind:      workintake.ReviewReactionComment,
		ReviewID:  comment.GetPullRequestReviewID(),
		CommentID: comment.GetID(),
		Author:    comment.GetUser().GetLogin(),
		Body:      comment.GetBody(),
		Path:      comment.GetPath(),
		Line:      comment.GetLine(),
	}
	r.deliverReaction(w, req, reaction)
}

func (r *Receiver) deliverReaction(w http.ResponseWriter, req *http.Request, reaction workintake.ReviewCommentEnvelope) {
	if err := r.reactionPublish(req.Context(), reaction); err != nil {
		r.log.Error("publish reaction", "key", reaction.IdempotencyKey(), "err", err)
		http.Error(w, "publish failed", http.StatusInternalServerError)
		return
	}
	r.recordReactionPublish()
	w.WriteHeader(http.StatusAccepted)
}

// allowReactionDelivery consumes one reaction slot for the remote source.
// A fixed window keeps the bookkeeping trivial; the failure mode is a burst
// slightly over one window, which the consumer's dedup and round cap bound.
func (r *Receiver) allowReactionDelivery(remote string) bool {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, v := range r.reactionLimiter {
		if now.Sub(v.start) > reactionRateBucketTTL {
			delete(r.reactionLimiter, k)
		}
	}
	w, ok := r.reactionLimiter[remote]
	if !ok || now.Sub(w.start) > reactionRateWindow {
		r.reactionLimiter[remote] = &reactionWindow{start: now, count: 0}
		w = r.reactionLimiter[remote]
	}
	w.count++
	return w.count <= reactionRateLimit
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
