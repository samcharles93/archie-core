package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	streamName   = "ARCHIE_TASKS"
	consumerName = "archie-daemon"
	msgPrefix    = "archie:" // for Nats-Msg-Id dedup
	dedupWindow  = 2 * time.Minute
	pollTimeout  = 2 * time.Second
)

// TaskMessage is the NATS payload for a discovered issue.
type TaskMessage struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Labels string `json:"labels"` // comma-separated
	// Identity is the archie identity whose forge poll discovered this
	// issue (empty for single-identity deployments). Carried through so
	// the consuming daemon can enqueue the task with the right owner.
	Identity string `json:"identity,omitempty"`
}

// Client manages the NATS connection and JetStream resources.
type Client struct {
	conn     *nats.Conn
	js       jetstream.JetStream
	stream   jetstream.Stream
	consumer jetstream.Consumer
	log      *slog.Logger
}

// Connect dials url, sets up the JetStream stream and pull consumer.
// The stream is created with WorkQueue retention and file storage.
func Connect(ctx context.Context, url, token string, log *slog.Logger) (*Client, error) {
	var opts []nats.Option
	if token != "" {
		opts = append(opts, nats.Token(token))
	}
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats jetstream: %w", err)
	}

	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       streamName,
		Subjects:   []string{"archie.task.>", "archie.agent.>"},
		Storage:    jetstream.FileStorage,
		Retention:  jetstream.WorkQueuePolicy,
		Duplicates: dedupWindow,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats stream: %w", err)
	}

	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:              consumerName,
		Durable:           consumerName,
		FilterSubject:     "archie.task.>",
		AckPolicy:         jetstream.AckExplicitPolicy,
		MaxDeliver:        3,
		AckWait:           5 * time.Minute,
		InactiveThreshold: 24 * time.Hour,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats consumer: %w", err)
	}

	return &Client{conn: nc, js: js, stream: stream, consumer: consumer, log: log}, nil
}

// Conn returns the underlying NATS connection for core NATS operations
// (inbox subscriptions, direct publishes).
func (c *Client) Conn() *nats.Conn { return c.conn }

// PublishMsg publishes a message to JetStream with optional headers.
func (c *Client) PublishMsg(ctx context.Context, msg *nats.Msg) (*jetstream.PubAck, error) {
	return c.js.PublishMsg(ctx, msg)
}

// Close drains and closes the NATS connection.
func (c *Client) Close() { c.conn.Close() }

// NewReplyInbox creates a unique inbox subject and returns a synchronous
// subscription configured to auto-unsubscribe after one message.
func (c *Client) NewReplyInbox() (*nats.Subscription, error) {
	sub, err := c.conn.SubscribeSync(c.conn.NewInbox())
	if err != nil {
		return nil, err
	}
	if err := sub.AutoUnsubscribe(1); err != nil {
		_ = sub.Unsubscribe()
		return nil, err
	}
	return sub, nil
}

// PublishTask publishes a discovered issue to the appropriate NATS subject.
// The Nats-Msg-Id header enables JetStream dedup within the dedup window.
func (c *Client) PublishTask(ctx context.Context, owner, repo string, number int, title, body, labels, identity string) error {
	msg := TaskMessage{
		Owner:    owner,
		Repo:     repo,
		Number:   number,
		Title:    title,
		Body:     body,
		Labels:   labels,
		Identity: identity,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	subject := SubjectForLabels(splitLabels(labels))
	headers := nats.Header{}
	msgID := fmt.Sprintf("%s%s/%s/%d", msgPrefix, owner, repo, number)
	headers.Set("Nats-Msg-Id", msgID)

	_, err = c.js.PublishMsg(ctx, &nats.Msg{
		Subject: subject,
		Data:    data,
		Header:  headers,
	})
	if err != nil {
		return fmt.Errorf("nats publish: %w", err)
	}
	return nil
}

// Fetch retrieves one task message from the pull consumer.
// Returns nil if no message is available within the poll timeout.
func (c *Client) Fetch(ctx context.Context) (jetstream.Msg, error) {
	batch, err := c.consumer.Fetch(1, jetstream.FetchMaxWait(pollTimeout))
	if err != nil {
		return nil, fmt.Errorf("nats fetch: %w", err)
	}
	for msg := range batch.Messages() {
		if msg == nil {
			continue
		}
		if err := batch.Error(); err != nil {
			return nil, fmt.Errorf("nats fetch batch: %w", err)
		}
		return msg, nil
	}
	return nil, nil
}

// DecodeTask decodes a TaskMessage from a NATS message.
func DecodeTask(msg jetstream.Msg) (TaskMessage, error) {
	var tm TaskMessage
	if err := json.Unmarshal(msg.Data(), &tm); err != nil {
		return tm, fmt.Errorf("nats decode: %w", err)
	}
	return tm, nil
}

// splitLabels splits a comma-separated label string into a slice.
func splitLabels(labels string) []string {
	if labels == "" {
		return nil
	}
	return strings.Split(labels, ",")
}
