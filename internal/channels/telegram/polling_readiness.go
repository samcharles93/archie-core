package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const telegramPollTimeout = time.Minute

// pollReadinessClient observes the same successful getUpdates response the
// Telegram library is about to decode. The library keeps polling errors inside
// its worker goroutine, so the HTTP boundary is the only place Start can prove
// that long polling is actually usable instead of merely spawned.
type pollReadinessClient struct {
	client    bot.HttpClient
	onRunning func()
	reported  atomic.Bool
}

func (c *pollReadinessClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.client.Do(req)
	if err != nil || c.reported.Load() || !strings.HasSuffix(req.URL.Path, "/getUpdates") {
		return resp, err
	}

	body, err := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read Telegram polling response: %w", err)
	}
	resp.Body = &replayBody{Reader: bytes.NewReader(body), closeErr: closeErr}
	if successfulPollResponse(body) && c.reported.CompareAndSwap(false, true) && c.onRunning != nil {
		c.onRunning()
	}
	return resp, nil
}

func successfulPollResponse(body []byte) bool {
	var response struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &response); err != nil || !response.OK {
		return false
	}
	var updates []*models.Update
	return json.Unmarshal(response.Result, &updates) == nil
}

type replayBody struct {
	*bytes.Reader
	closeErr error
}

func (b *replayBody) Close() error {
	err := b.closeErr
	b.closeErr = nil
	return err
}
