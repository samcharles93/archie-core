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
package webhook

import (
	"context"
	"io"
	"log/slog"
	"net/http"

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
}

// New returns an unstarted Receiver.
func New(secret, trigger, label, botUser string, publish PublishFunc, log *slog.Logger) *Receiver {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Receiver{
		secret:  secret,
		trigger: trigger,
		label:   label,
		botUser: botUser,
		publish: publish,
		log:     log.With("component", "forge-webhook"),
	}
}

// ServeHTTP handles one forge webhook delivery.
func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
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
