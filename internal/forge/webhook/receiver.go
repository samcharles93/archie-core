// Package webhook receives forge issue and review webhooks, verifies their
// HMAC, has the forge's parser decode them and publishes them through the
// poller's publish path. Only
// application/json payloads are accepted. A GET returns delivery Status.
package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/webhookguard"
)

// maxBodyBytes bounds a webhook payload before it is read. GitHub webhook
// bodies are small; the cap exists so a hostile sender cannot exhaust memory.
const maxBodyBytes = 1 << 20 // 1 MiB

// PublishFunc delivers a decoded, dispatch-matched task envelope to the
// daemon's publish path. The composition root wires it to
// (*daemon.Daemon).PublishTask, the same enqueue path the poller uses.
type PublishFunc func(ctx context.Context, task workintake.TaskEnvelope) error

// WithdrawFunc declines the work an issue no longer asks for.
type WithdrawFunc func(ctx context.Context, owner, repo string, number int, reason string) error

// ReactionPublishFunc publishes one review reaction.
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
	parser          forge.WebhookParser
	trigger         string
	label           string
	botUser         string
	publish         PublishFunc
	reactionPublish ReactionPublishFunc
	withdraw        WithdrawFunc
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
func New(secret string, parser forge.WebhookParser, trigger, label, botUser string, publish PublishFunc, reactionPublish ReactionPublishFunc, withdraw WithdrawFunc, log *slog.Logger) *Receiver {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Receiver{
		secret:          secret,
		parser:          parser,
		trigger:         trigger,
		label:           label,
		botUser:         botUser,
		publish:         publish,
		reactionPublish: reactionPublish,
		withdraw:        withdraw,
		reactionLimiter: map[string]*reactionWindow{},
		log:             log.With("component", "forge-webhook"),
		startedAt:       time.Now(),
	}
}

// Status counts authenticated deliveries and published tasks.
// LastReceivedAt is nil before the first delivery.
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

	headers := make(map[string]string, len(req.Header))
	for name := range req.Header {
		headers[name] = req.Header.Get(name)
	}
	event, err := r.parser.ParseWebhook(req.Context(), headers, body)
	if errors.Is(err, forge.ErrBadWebhook) {
		r.log.Warn("parse webhook", "err", err)
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if err != nil {
		r.log.Error("parse webhook", "err", err)
		http.Error(w, "parse unavailable", http.StatusServiceUnavailable)
		return
	}
	// Authenticated and parseable: this is a genuine forge delivery,
	// independent of whether it turns out to be dispatch-eligible.
	r.recordDelivery()

	switch {
	case event.Issue != nil:
		r.serveIssueEvent(w, req, event.Issue)
	case event.Review != nil:
		r.serveReviewEvent(w, req, event.Review)
	case event.ReviewComment != nil:
		r.serveReviewCommentEvent(w, req, event.ReviewComment)
	default:
		// Not an event this receiver decodes (ping, push, etc.).
		// Acknowledge and ignore.
		w.WriteHeader(http.StatusAccepted)
	}
}

// serveIssueEvent is the receiver's original contract: an eligible issue
// event becomes a task envelope on the task path.
func (r *Receiver) serveIssueEvent(w http.ResponseWriter, req *http.Request, issueEvent *forge.IssueEvent) {
	task, ok := r.taskFromEvent(issueEvent)
	if !ok {
		// Delivered but not eligible work (PR, closed, unrelated action,
		// dispatch mismatch). A withdrawal declines the work it queued;
		// anything else is acknowledged without publishing.
		if reason, withdrawn := r.withdrawal(issueEvent); withdrawn && r.withdraw != nil {
			if err := r.withdraw(req.Context(), issueEvent.Owner, issueEvent.Repo, issueEvent.Number, reason); err != nil {
				r.log.Error("withdraw task", "repo", issueEvent.Owner+"/"+issueEvent.Repo, "issue", issueEvent.Number, "err", err)
				http.Error(w, "withdraw failed", http.StatusInternalServerError)
				return
			}
		}
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
func (r *Receiver) serveReviewEvent(w http.ResponseWriter, req *http.Request, e *forge.ReviewEvent) {
	if e.Action != "submitted" {
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
	reaction := workintake.ReviewCommentEnvelope{
		Owner:    e.Owner,
		Repo:     e.Repo,
		PRNumber: e.PRNumber,
		Kind:     workintake.ReviewReactionReview,
		ReviewID: e.ReviewID,
		Author:   e.Author,
		State:    e.State,
		Body:     e.Body,
	}
	r.deliverReaction(w, req, reaction)
}

// serveReviewCommentEvent publishes an inline comment as a reaction. The
// comment carries its parent review's ID, so the consumer can collect it
// into that review's unit (decision 5) rather than starting a round of its
// own.
func (r *Receiver) serveReviewCommentEvent(w http.ResponseWriter, req *http.Request, e *forge.ReviewCommentEvent) {
	if e.Action != "created" {
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
	reaction := workintake.ReviewCommentEnvelope{
		Owner:     e.Owner,
		Repo:      e.Repo,
		PRNumber:  e.PRNumber,
		Kind:      workintake.ReviewReactionComment,
		ReviewID:  e.ReviewID,
		CommentID: e.CommentID,
		Author:    e.Author,
		Body:      e.Body,
		Path:      e.Path,
		Line:      e.Line,
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

// withdrawal reports whether an issues event takes an issue out of dispatch:
// it was closed, or lost the label or assignee that made it work.
func (r *Receiver) withdrawal(event *forge.IssueEvent) (string, bool) {
	if event.PullRequest || event.Owner == "" || event.Repo == "" {
		return "", false
	}
	switch event.Action {
	case "closed":
		return "the issue was closed", true
	case "unlabeled", "unassigned":
		if !workintake.MatchesDispatch(r.trigger, r.label, r.botUser, event.Labels, event.Assignees) {
			return "the issue was " + event.Action, true
		}
	}
	return "", false
}

// taskFromEvent reports the task an issues event makes eligible, or
// (zero, false) when it is not eligible work.
func (r *Receiver) taskFromEvent(event *forge.IssueEvent) (workintake.TaskEnvelope, bool) {
	if event.PullRequest {
		return workintake.TaskEnvelope{}, false
	}
	// Only events that could newly make an issue eligible. unlabeled,
	// unassigned, closed, and edited never queue work.
	switch event.Action {
	case "opened", "reopened", "labeled", "assigned":
	default:
		return workintake.TaskEnvelope{}, false
	}
	if event.State != "open" {
		return workintake.TaskEnvelope{}, false
	}
	if !workintake.MatchesDispatch(r.trigger, r.label, r.botUser, event.Labels, event.Assignees) {
		return workintake.TaskEnvelope{}, false
	}
	if event.Owner == "" || event.Repo == "" {
		r.log.Warn("event missing repository", "action", event.Action)
		return workintake.TaskEnvelope{}, false
	}
	return workintake.TaskEnvelope{
		Owner:  event.Owner,
		Repo:   event.Repo,
		Number: event.Number,
		Title:  event.Title,
		Body:   event.Body,
		Labels: event.Labels,
		Kind:   workintake.KindForLabels(event.Labels),
	}, true
}
