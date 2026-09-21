// status_health.go is the composition-root bridge behind /status' health
// section: it reads the facts THIS process owns and hands /status a narrow
// report, so the gateway package never takes a daemon, pool or manager handle.
//
// The report carries a broker line and a chat-model line. Those are the facts
// the process that serves /status holds: setupGatewayChat is the sole
// production constructor of a Router and it runs in the Gateway, which owns
// neither a container pool, a poll loop nor a channel manager. A producer
// wired for one of the three would build a section no surface ever reads.
package archied

import (
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/gateway"
)

// providerOutcomeRecorder is the producer behind /status' chat-model line. It
// holds the outcome of the last chat-model call this process made, recorded at
// the one place every chat-model call passes through (sendChatTurn), so there
// is no second path that could report a different answer.
//
// It never dials a provider: the line reports what happened, and a fresh probe
// inside a chat command would report the probe's own reachability while
// costing the command a network round trip.
type providerOutcomeRecorder struct {
	mu        sync.Mutex
	attempted bool
	outcome   gateway.ChatModelOutcome
}

func newProviderOutcomeRecorder() *providerOutcomeRecorder { return &providerOutcomeRecorder{} }

// record stores one finished call's outcome. err is the call's own error, nil
// when it succeeded. A nil recorder records nothing -- call paths built
// without one (tests, minimal setups) must not panic.
func (r *providerOutcomeRecorder) record(model string, err error) {
	r.recordAt(model, err, time.Now().UTC())
}

// recordAt stores one call's outcome, keeping whichever is most recent.
//
// The completion time is taken by the caller, before the lock, so it reports
// when the call actually finished rather than when this goroutine won the
// mutex. That ordering is not the same as the lock's: two concurrent calls can
// reach the lock in the opposite order to their completion, and an
// unconditional store would then leave /status reporting an older call as the
// latest one. Keeping the newer of the two is what makes the reported outcome
// true regardless of scheduling.
func (r *providerOutcomeRecorder) recordAt(model string, err error, at time.Time) {
	if r == nil {
		return
	}
	outcome := gateway.ChatModelOutcome{Model: model, At: at}
	if err != nil {
		outcome.Err = err.Error()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.attempted && outcome.At.Before(r.outcome.At) {
		return
	}
	r.outcome = outcome
	r.attempted = true
}

// LastChatModelOutcome reports the most recent recorded call, and whether any
// call has been recorded at all.
func (r *providerOutcomeRecorder) LastChatModelOutcome() (gateway.ChatModelOutcome, bool) {
	if r == nil {
		return gateway.ChatModelOutcome{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.outcome, r.attempted
}

// statusHealth assembles gateway.HealthReport from this process's own
// subsystems. Every source is optional; a nil source means this process holds
// no truthful input for that fact and the section is left out of the report.
//
// The broker source is a function rather than a captured value because the
// connection is process state: the daemon's shared eventbus client in one
// composition, the Gateway's own task-actions connection in the other.
type statusHealth struct {
	broker    func() (connected, ok bool)
	chatModel *providerOutcomeRecorder
}

func (s statusHealth) Health() gateway.HealthReport {
	var report gateway.HealthReport
	if s.broker != nil {
		if connected, ok := s.broker(); ok {
			report.Broker = &gateway.BrokerHealth{Connected: connected}
		}
	}
	if s.chatModel != nil {
		outcome, attempted := s.chatModel.LastChatModelOutcome()
		report.ChatModel = &gateway.ChatModelHealth{Attempted: attempted, Outcome: outcome}
	}
	return report
}

// newStatusHealth builds the health source this process serves. It carries the
// two facts a Router in this process can render: the process's own broker
// connection, and the last chat-model call it made. A source whose subsystem
// is absent here stays nil, and /status omits that line.
//
// The pool, the poll loop and the channel managers stay out even in the
// composition that holds them. setupGatewayChat is the only production
// constructor of a Router; it runs in the Gateway, which holds none of the
// three, so a producer reading them would build a section no /status reply can
// carry.
func newStatusHealth(b *boot) gateway.HealthSource {
	return statusHealth{
		chatModel: b.providerOutcomes,
		broker: func() (bool, bool) {
			switch {
			case b.natsClient != nil:
				return b.natsClient.Connected(), true
			case b.taskActionsConn != nil:
				return b.taskActionsConn.IsConnected(), true
			default:
				return false, false
			}
		},
	}
}
