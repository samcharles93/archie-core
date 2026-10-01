package channels

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// defaultEphemeralDeleteTimeout bounds one retraction. It is independent of
// the turn's own context, which may already be cancelled by the time the TTL
// elapses: the message was sent, so it still has to be removed.
const defaultEphemeralDeleteTimeout = 30 * time.Second

// SendFunc delivers one message and returns the platform's view of it, with
// at least the identifier a later retraction addresses. A sender whose
// platform cannot name a delivered message leaves ID empty, and a retraction
// of that message is then skipped rather than attempted blindly.
type SendFunc func(ctx context.Context, event messaging.MessageEvent) (messaging.MessageEvent, error)

// EphemeralSender delivers EphemeralReplies and retracts them after their TTL.
//
// The retraction runs on its own goroutine, so a TTL that has not elapsed
// never holds the turn that sent the reply. after is injected so the TTL
// fires in tests without sleeping; nil selects time.After, which is the
// production shape.
type EphemeralSender struct {
	after   func(time.Duration) <-chan time.Time
	timeout time.Duration
	log     *slog.Logger
	wg      sync.WaitGroup
}

// NewEphemeralSender returns a sender whose retractions wait on after.
func NewEphemeralSender(log *slog.Logger, after func(time.Duration) <-chan time.Time) *EphemeralSender {
	if after == nil {
		after = time.After
	}
	if log == nil {
		log = slog.Default()
	}
	return &EphemeralSender{
		after:   after,
		timeout: defaultEphemeralDeleteTimeout,
		log:     log,
	}
}

// Send delivers reply.Event through send. When sender reports the Delete
// capability, the delivered message is retracted once reply.TTL elapses;
// otherwise the reply is left in place and the fact is logged once, at debug
// level. A platform that cannot delete must never fail the turn, so the
// degrade returns the send's outcome rather than an error.
//
// A send error is returned unchanged and no retraction is scheduled: there
// is no message to remove. A non-positive TTL or a send that reported no
// message identifier also schedules nothing.
func (e *EphemeralSender) Send(ctx context.Context, sender any, reply messaging.EphemeralReply, send SendFunc) (messaging.MessageEvent, error) {
	sent, err := send(ctx, reply.Event)
	if err != nil {
		return sent, err
	}
	// A reply with no lifetime, or a send the platform could not name, has
	// nothing to schedule: there is either no deadline or no message to
	// address.
	if reply.TTL <= 0 || sent.ID == "" {
		return sent, nil
	}

	deleter, ok := messaging.DeleterOf(sender)
	if !ok {
		e.log.Debug("channel cannot retract an ephemeral reply; leaving it in place",
			"platform", reply.Event.Platform, "type", reply.Event.Type)
		return sent, nil
	}

	e.wg.Go(func() {
		<-e.after(reply.TTL)
		// The turn's context may already be cancelled by the time the TTL
		// elapses -- a delivered message is still retracted -- so the
		// retraction carries its own bounded, uncancellable context.
		delCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.timeout)
		defer cancel()
		if err := deleter.DeleteMessage(delCtx, sent); err != nil {
			e.log.Warn("ephemeral reply retraction failed",
				"platform", reply.Event.Platform, "error", err)
		}
	})
	return sent, nil
}

// Wait blocks until every retraction started so far has finished. Tests only:
// production callers never wait on a retraction's own TTL.
func (e *EphemeralSender) Wait() {
	e.wg.Wait()
}
