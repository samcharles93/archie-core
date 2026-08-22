// Package minimax generates video through MiniMax's video-generation API:
// https://platform.minimax.io/docs/api-reference/video-generation-v2-create
//
// Generation is asynchronous on MiniMax's side (submit a job, poll for a
// result), but archied's tool subsystem has no async-tool flow control (see
// tools.ToolEntry.IsAsync's doc comment), so GenerateAndWait blocks the
// calling goroutine  --  a tool call, not the model's own generating
// goroutine  --  submitting and polling until the job reaches a terminal
// status or MaxWait elapses.
package minimax

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// defaultBaseURL is MiniMax's production API host. Overridable for
	// tests and for an operator pointed at a proxy.
	defaultBaseURL = "https://api.minimax.io"

	// defaultModel is the only model documented for this endpoint.
	defaultModel = "MiniMax-H3"

	// defaultResolution and defaultDuration match the middle of MiniMax's
	// documented ranges (768P/2K; 4-15s) rather than the extremes, so an
	// unconfigured request is neither the cheapest-lowest-quality nor the
	// most expensive option by default.
	defaultResolution = "768P"
	defaultDuration   = 6

	// defaultPollInterval and defaultMaxWait bound one generation. MiniMax
	// documents no expected completion time, so these are a starting
	// guess: frequent enough to feel responsive, bounded low enough that
	// a stuck job fails a chat turn in minutes rather than hanging it.
	defaultPollInterval = 5 * time.Second
	defaultMaxWait      = 5 * time.Minute

	// defaultTimeout bounds one HTTP call (create or a single query), not
	// the whole generate-and-wait loop  --  that is MaxWait.
	defaultTimeout = 30 * time.Second
)

// Config controls the client. The zero value is disabled.
type Config struct {
	// Enabled advertises the tool. When false, Tool returns nil.
	Enabled bool

	// APIKey authenticates every request as a Bearer token.
	APIKey string

	// BaseURL overrides the API host. Empty uses defaultBaseURL.
	BaseURL string

	// Timeout bounds one HTTP call. Zero uses defaultTimeout.
	Timeout time.Duration

	// PollInterval is the delay between status queries. Zero uses
	// defaultPollInterval.
	PollInterval time.Duration

	// MaxWait bounds the whole submit-and-poll loop. Zero uses
	// defaultMaxWait.
	MaxWait time.Duration

	// Sleep delays between polls. Nil uses time.Sleep. Tests inject a
	// no-op or counting stand-in so a poll loop that spans MaxWait does
	// not actually take that long to run.
	Sleep func(time.Duration)
}

// Client generates video through the MiniMax API.
type Client struct {
	cfg  Config
	http *http.Client
}

// New builds a Client, filling unset Config fields with their defaults.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.MaxWait <= 0 {
		cfg.MaxWait = defaultMaxWait
	}
	if cfg.Sleep == nil {
		cfg.Sleep = time.Sleep
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
}

// GenerateRequest is one text-to-video job. Image-to-video (image_url
// content items) is not implemented: the first consumer is text-only
// generation, and adding image references is a schema-and-routing change
// this can grow into rather than needing up front.
type GenerateRequest struct {
	// Prompt is the required text description (MiniMax caps this at 7000
	// characters; that is not separately enforced here, the API rejects
	// an oversized prompt with a 400 the caller sees).
	Prompt string

	// Resolution is "768P" or "2K". Empty uses defaultResolution.
	Resolution string

	// Duration is the clip length in seconds, 4-15. Zero uses
	// defaultDuration.
	Duration int

	// Ratio is the aspect ratio (see the package doc's API reference for
	// the allowed set). Empty uses "adaptive", MiniMax's own default.
	Ratio string
}

// GenerateAndWait submits req and blocks until MiniMax reports a terminal
// status, returning the hosted video URL on success.
func (c *Client) GenerateAndWait(ctx context.Context, req GenerateRequest) (string, error) {
	taskID, err := c.create(ctx, req)
	if err != nil {
		return "", err
	}

	// elapsed is tracked from the injected Sleep calls rather than a real
	// time.Now() deadline, so a test that fakes Sleep as an instant
	// counter also makes this loop terminate instantly instead of
	// spinning in real wall-clock time until an unfaked clock catches up.
	var elapsed time.Duration
	for {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("minimax: task %s: %w", taskID, err)
		}

		task, err := c.query(ctx, taskID)
		if err != nil {
			return "", err
		}

		switch task.Status {
		case "succeeded":
			if task.Content.URL == "" {
				return "", fmt.Errorf("minimax: task %s succeeded but reported no video url", taskID)
			}
			return task.Content.URL, nil
		case "failed", "cancelled":
			return "", fmt.Errorf("minimax: task %s %s: %s", taskID, task.Status, task.errorMessage())
		}

		if elapsed >= c.cfg.MaxWait {
			return "", fmt.Errorf("minimax: task %s timed out after %v waiting for completion (last status %q)",
				taskID, c.cfg.MaxWait, task.Status)
		}
		c.cfg.Sleep(c.cfg.PollInterval)
		elapsed += c.cfg.PollInterval
	}
}

type createResponse struct {
	TaskID string `json:"task_id"`
}

// create submits req and returns the task ID MiniMax assigned.
func (c *Client) create(ctx context.Context, req GenerateRequest) (string, error) {
	resolution := req.Resolution
	if resolution == "" {
		resolution = defaultResolution
	}
	duration := req.Duration
	if duration == 0 {
		duration = defaultDuration
	}
	ratio := req.Ratio
	if ratio == "" {
		ratio = "adaptive"
	}

	body := map[string]any{
		"model":      defaultModel,
		"resolution": resolution,
		"duration":   duration,
		"ratio":      ratio,
		"content": []map[string]string{
			{"type": "text", "text": req.Prompt},
		},
	}

	var out createResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v2/video_generation", body, &out); err != nil {
		return "", err
	}
	if out.TaskID == "" {
		return "", errors.New("minimax: create response carried no task_id")
	}
	return out.TaskID, nil
}

// taskStatus is the polled state of one generation job.
type taskStatus struct {
	Status string `json:"status"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Content struct {
		URL string `json:"url"`
	} `json:"content"`
}

// errorMessage reports the platform's own failure detail, falling back to
// a placeholder when MiniMax reported a terminal failure with no error
// object  --  which the docs don't rule out.
func (t taskStatus) errorMessage() string {
	if t.Error == nil || t.Error.Message == "" {
		return "no error detail reported"
	}
	return t.Error.Message
}

type queryResponse struct {
	Task taskStatus `json:"task"`
}

// query fetches the current status of taskID.
func (c *Client) query(ctx context.Context, taskID string) (taskStatus, error) {
	var out queryResponse
	path := "/v2/query/video_generation/" + taskID
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return taskStatus{}, err
	}
	return out.Task, nil
}

// apiError is the documented error envelope MiniMax returns on a non-200
// response.
type apiError struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// doJSON issues one request and decodes a 200 response into out. A non-200
// response is parsed as apiError and returned as an error carrying
// MiniMax's own message, so a create/query failure is never reported as a
// bare HTTP status.
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("minimax: encode request: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.BaseURL, "/")+path, reader)
	if err != nil {
		return fmt.Errorf("minimax: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("minimax: request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("minimax: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr apiError
		if err := json.Unmarshal(data, &apiErr); err == nil && apiErr.Error.Message != "" {
			return fmt.Errorf("minimax: %s (http %d)", apiErr.Error.Message, resp.StatusCode)
		}
		return fmt.Errorf("minimax: request failed with http %d: %s", resp.StatusCode, string(data))
	}

	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("minimax: decode response: %w", err)
	}
	return nil
}
