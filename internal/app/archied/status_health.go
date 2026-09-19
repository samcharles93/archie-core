// status_health.go is the composition-root bridge behind /status' health
// section: it reads whichever subsystems THIS process owns and hands /status a
// narrow report, so the gateway package never takes a daemon, pool or manager
// handle.
//
// The split matters because archie's operator surface spans two processes.
// The daemon owns the forge poll loop, the container pool and the channel
// manager; the standalone Gateway owns none of those. Each process therefore
// reports what it actually has and leaves the rest absent -- /status shows a
// shorter list in the Gateway, not a list of zeroes.
package archied

import (
	"cmp"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/channels/status"
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
	if r == nil {
		return
	}
	outcome := gateway.ChatModelOutcome{Model: model, At: time.Now().UTC()}
	if err != nil {
		outcome.Err = err.Error()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
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
// The sources are functions rather than captured values because they change
// over the life of the process: the container pool's occupancy, the channel
// states, and the poll timestamp all move, and the two NATS connections can
// drop. Capturing them at construction would freeze /status on the daemon's
// boot-time state -- occupancy would read 0/4 forever, and every channel would
// read as configured-but-not-started.
type statusHealth struct {
	broker     func() (connected, ok bool)
	containers func() (active, capacity int, ok bool)
	channels   func() (channels []gateway.ChannelHealth, ok bool)
	chatModel  *providerOutcomeRecorder
	lastPoll   func() (at time.Time, ok bool)
}

func (s statusHealth) Health() gateway.HealthReport {
	var report gateway.HealthReport
	if s.broker != nil {
		if connected, ok := s.broker(); ok {
			report.Broker = &gateway.BrokerHealth{Connected: connected}
		}
	}
	if s.containers != nil {
		if active, capacity, ok := s.containers(); ok {
			report.Containers = &gateway.ContainerHealth{Active: active, Cap: capacity}
		}
	}
	if s.channels != nil {
		if channels, ok := s.channels(); ok {
			report.Channels = channels
		}
	}
	if s.chatModel != nil {
		outcome, attempted := s.chatModel.LastChatModelOutcome()
		report.ChatModel = &gateway.ChatModelHealth{Attempted: attempted, Outcome: outcome}
	}
	if s.lastPoll != nil {
		if at, ok := s.lastPoll(); ok {
			report.LastPoll = &at
		}
	}
	return report
}

// newStatusHealth builds the health source for this process. Sources whose
// subsystem is absent here (a deployment with no container pool, the Gateway
// process with no daemon) stay nil, and /status omits them.
//
// The closures read boot's fields at call time on purpose. Gateways are wired
// before the daemon is built, so boot.d is nil when this runs -- capturing it
// here would leave the poll section permanently absent in the one process that
// actually polls, with nothing at runtime to show the wire was dead.
func newStatusHealth(b *boot) gateway.HealthSource {
	s := statusHealth{
		chatModel: b.providerOutcomes,
		containers: func() (int, int, bool) {
			if b.containerPool == nil {
				return 0, 0, false
			}
			return b.containerPool.Active(), b.containerPool.Cap(), true
		},
		channels: func() ([]gateway.ChannelHealth, bool) {
			if b.channelManager == nil {
				return nil, false
			}
			return channelHealth(b.channelManager), true
		},
		lastPoll: func() (time.Time, bool) {
			if b.d == nil {
				return time.Time{}, false
			}
			return b.d.LastPollAt(), true
		},
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
	return s
}

// channelHealth projects the channel manager's snapshot into /status' view, so
// the gateway package does not depend on the channels package. It mirrors
// readiness.go's channelStates projection for the same reason.
//
// An unset display name falls back to the channel id: a line reading
// " running" would leave an operator unable to tell which channel is broken,
// which is the one thing this line exists to say.
//
// Detail is carried through whenever it is set, exactly as the dashboard reads
// it. It is one field serving two purposes -- a descriptor's standing
// configuration caveat ("token set, but the allowlist is empty") and a runtime
// failure reason -- and the manager keeps the last non-empty one, so a channel
// that failed and later recovered still shows the old reason. Filtering by
// state here would drop the caveat that matters most on a *running* channel
// (the empty allowlist one), so the split belongs at the producer, not here.
func channelHealth(m *status.Manager) []gateway.ChannelHealth {
	if m == nil {
		return nil
	}
	snapshot := m.Snapshot()
	out := make([]gateway.ChannelHealth, 0, len(snapshot))
	for _, s := range snapshot {
		out = append(out, gateway.ChannelHealth{
			Name:   cmp.Or(s.Name, s.ID),
			State:  string(s.State),
			Detail: s.Detail,
		})
	}
	return out
}
