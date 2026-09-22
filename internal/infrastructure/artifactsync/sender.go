// Package artifactsync publishes artifacts to the offloaded.dev collaborative
// editor: archie-core generates and sends, the editor hosts, renders and
// shares. It is infrastructure in the Courier posture — one method, injected
// by the composition root, absent when configuration does not resolve.
package artifactsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const contractFormat = "offloaded-collab-artifact"

// Artifact is one publishable artifact: the rendered source plus the metadata
// the receiving contract carries.
type Artifact struct {
	Title  string
	Source string // CommonMark for prose, raw source for runnable formats
	Meta   Meta
}

// Meta is the artifact block of the receiving contract: shape, attribution and
// idempotency fields. Origin is named "source" on the wire; the top-level
// Source field carries the content.
type Meta struct {
	Format     string    `json:"format"`
	Category   string    `json:"category"`
	Entry      string    `json:"entry,omitempty"` // required for html/react/vue, one file per artifact
	Visibility string    `json:"visibility"`
	Egress     string    `json:"egress"`
	Actor      string    `json:"actor"`
	Origin     string    `json:"source"` // the producing subsystem, e.g. "pr-review"
	RequestID  string    `json:"requestId"`
	Version    string    `json:"version,omitempty"`
	SentAt     time.Time `json:"sentAt"`
	Task       *Task     `json:"task,omitempty"`
}

// Task carries the forge coordinates the artifact was produced for.
type Task struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Task   string `json:"task,omitempty"`
}

// Sender publishes one artifact and returns the editor deep link for it.
type Sender interface {
	Publish(ctx context.Context, a Artifact) (string, error)
}

// Client is the Sender against a deployed editor.
type Client struct {
	baseURL string
	token   string
	client  *http.Client
	// wait is the base delay between 503 retries, doubled per attempt.
	wait time.Duration
}

// NewClient builds a Client against the editor origin. It returns nil when
// baseURL is empty, which is the disabled posture: the composition root skips
// wiring the sender entirely rather than carrying one that cannot be called.
func NewClient(baseURL, token string) *Client {
	if baseURL == "" {
		return nil
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 20 * time.Second},
		wait:    250 * time.Millisecond,
	}
}

// publishBody is the frozen receiving contract. Its shape is the public API;
// changing it is a contract change with the editor, not a refactor.
type publishBody struct {
	Format   string `json:"format"`
	Title    string `json:"title"`
	Source   string `json:"source"`
	Artifact Meta   `json:"artifact"`
}

type publishResponse struct {
	OK  bool   `json:"ok"`
	ID  string `json:"id"`
	URL string `json:"url"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// Publish posts the artifact once. The full source is always sent: versions of
// record are full snapshots stored per publish, never diffs.
func (c *Client) Publish(ctx context.Context, a Artifact) (string, error) {
	if c == nil {
		return "", fmt.Errorf("artifactsync: sender is not configured")
	}
	body := publishBody{Format: contractFormat, Title: a.Title, Source: a.Source, Artifact: a.Meta}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("artifactsync: encode artifact: %w", err)
	}

	const attempts = 4 // the initial attempt plus three retries
	var lastErr error
	for attempt := range attempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("artifactsync: publish %q: %w", a.RequestID(), ctx.Err())
			case <-time.After(c.wait << (attempt - 1)):
			}
		}
		url, retryable, err := c.postOnce(ctx, payload)
		if err == nil {
			return url, nil
		}
		lastErr = err
		if !retryable {
			return "", err
		}
	}
	return "", fmt.Errorf("artifactsync: publish %q: %w", a.RequestID(), lastErr)
}

// RequestID is the artifact's own idempotency key, for logging and for callers
// that compose one from the producer's natural key.
func (a Artifact) RequestID() string { return a.Meta.RequestID }

// postOnce performs one HTTP attempt. retryable marks failures worth another
// attempt: 503 and transport errors. Client errors are configuration or shape
// mistakes and are surfaced immediately with the field reason.
func (c *Client) postOnce(ctx context.Context, payload []byte) (url string, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/artifacts", bytes.NewReader(payload))
	if err != nil {
		return "", false, fmt.Errorf("artifactsync: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", true, fmt.Errorf("artifactsync: post artifact: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated:
		var out publishResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return "", false, fmt.Errorf("artifactsync: decode publish response: %w", err)
		}
		if out.URL == "" {
			return "", false, fmt.Errorf("artifactsync: publish response carries no url")
		}
		return out.URL, false, nil
	case http.StatusServiceUnavailable:
		return "", true, fmt.Errorf("artifactsync: editor store unavailable (503)")
	case http.StatusUnauthorized:
		return "", false, fmt.Errorf("artifactsync: editor rejected the service token (401); check [artifacts].token")
	case http.StatusBadRequest:
		reason := "unknown"
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var e errorResponse
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			reason = e.Error
		}
		return "", false, fmt.Errorf("artifactsync: editor rejected the artifact (400): %s", reason)
	default:
		return "", true, fmt.Errorf("artifactsync: unexpected publish status %d", resp.StatusCode)
	}
}
