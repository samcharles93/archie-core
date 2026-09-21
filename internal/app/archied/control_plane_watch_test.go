package archied

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/gateway"
)

// The stream ends these tests drive. A control-plane watch stream stops for
// both of them and neither is answered on the client side: a broken stream
// arrives as an error from Recv, and a stream the server closed arrives as
// io.EOF, which controlplane.Client emits no update for at all -- it just
// closes the channel.
var watchStreamEnds = []struct {
	name string
	end  error
}{
	{"the stream fails", status.Error(codes.Unavailable, "state store restarting")},
	{"the server closes the stream", io.EOF},
}

// watchScript is one stream the stub hands a Watch call: the resources it
// sends, then what ends it. A nil end leaves the stream open until the caller
// goes away, which is what a healthy idle stream looks like.
type watchScript struct {
	resources []*pb.Resource
	end       error
}

// watchStub is the control-plane end of a watch that ends once and is then
// re-established. Query answers the boot read for each kind, Watch hands each
// successive call the next scripted stream -- filtered to the versions newer
// than the one the call resumed after, as the real server filters them -- and
// the version each call resumed after is recorded so a test can assert the
// reconnect resumes rather than replays. Calls past the script stay open.
type watchStub struct {
	boot    map[string]*pb.Resource
	scripts map[string][]watchScript

	mu       sync.Mutex
	resumed  map[string][]int64
	requests map[string][]*pb.WatchRequest
}

func (s *watchStub) Catalog(context.Context, *pb.CatalogRequest, ...grpc.CallOption) (*pb.CatalogResponse, error) {
	return &pb.CatalogResponse{}, nil
}

func (s *watchStub) Query(_ context.Context, request *pb.QueryRequest, _ ...grpc.CallOption) (*pb.QueryResponse, error) {
	resource, ok := s.boot[request.Kind]
	if !ok {
		return nil, status.Error(codes.NotFound, "resource not found")
	}
	return &pb.QueryResponse{Resource: resource}, nil
}

func (*watchStub) History(context.Context, *pb.HistoryRequest, ...grpc.CallOption) (*pb.HistoryResponse, error) {
	panic("unexpected History")
}

func (*watchStub) Command(context.Context, *pb.CommandRequest, ...grpc.CallOption) (*pb.CommandResponse, error) {
	panic("unexpected Command")
}

func (s *watchStub) Watch(ctx context.Context, request *pb.WatchRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[pb.WatchResponse], error) {
	s.mu.Lock()
	if s.resumed == nil {
		s.resumed = map[string][]int64{}
	}
	if s.requests == nil {
		s.requests = map[string][]*pb.WatchRequest{}
	}
	s.resumed[request.Kind] = append(s.resumed[request.Kind], request.AfterVersion)
	s.requests[request.Kind] = append(s.requests[request.Kind], request)
	scripts := s.scripts[request.Kind]
	var next watchScript
	if len(scripts) > 0 {
		next = scripts[0]
		s.scripts[request.Kind] = scripts[1:]
	}
	s.mu.Unlock()
	// controlplane.Server.Watch serves only versions greater than the resume
	// point, so the stub does too. A stub that replayed its whole script would
	// hand a reconnect that asked from zero this process's own already-applied
	// document, and the double apply would go unnoticed.
	served := make([]*pb.Resource, 0, len(next.resources))
	for _, resource := range next.resources {
		if resource.Version > request.AfterVersion {
			served = append(served, resource)
		}
	}
	return &scriptStream{done: ctx.Done(), resources: served, end: next.end}, nil
}

// resumedAfter is the AfterVersion of every Watch call for a kind, in order.
func (s *watchStub) resumedAfter(kind string) []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.resumed[kind]...)
}

// awaitWatchCalls waits until n Watch calls have been made for kind. The watch
// makes the second one only after it has delivered the update that ended the
// first stream, so a test asserting what that update did *not* report has
// something to synchronise on: the delivery precedes the reopen.
func (s *watchStub) awaitWatchCalls(t *testing.T, kind string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(s.resumedAfter(kind)) < n {
		if time.Now().After(deadline) {
			t.Fatalf("watch calls for %s = %d, want %d", kind, len(s.resumedAfter(kind)), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// scriptStream is the client end of one scripted Watch stream. Recv replays the
// resources, then the stream's end: io.EOF for a server that closed it, the
// scripted failure for one that broke, or a wait for the caller to go away for
// a stream that is simply idle. The embedded ClientStream is nil, so any other
// call panics rather than pretending to work, as in the settingsWatchStub of
// control_plane_live_apply_test.go.
type scriptStream struct {
	grpc.ClientStream
	// done is the stream context's Done channel, held rather than the context
	// itself: an idle stream ends the way a real one does when its RPC is
	// cancelled.
	done      <-chan struct{}
	resources []*pb.Resource
	end       error
	next      int
}

func (s *scriptStream) Recv() (*pb.WatchResponse, error) {
	if s.next < len(s.resources) {
		resource := s.resources[s.next]
		s.next++
		return &pb.WatchResponse{Resource: resource}, nil
	}
	if s.end != nil {
		end := s.end
		s.end = nil
		return nil, end
	}
	<-s.done
	return nil, context.Canceled
}

// TestWorkflowExecutionSettingsWatchIsReEstablishedAfterTheStreamEnds is
// archie-core-yrmr. A watch goroutine is launched once, at boot, and a
// control-plane stream is not permanent: the State Store restarts, the
// connection drops, or the server closes the stream. A watch that ends on the
// first stream failure stops applying that kind for the rest of the process's
// life -- every later stored change is silently never applied and the process
// only recovers by restarting. The stream must be re-established and the later
// update delivered on it must be applied.
func TestWorkflowExecutionSettingsWatchIsReEstablishedAfterTheStreamEnds(t *testing.T) {
	// 25 steps and an hour, so an applied update is unmistakably not the value
	// the daemon booted with.
	updated := workflow.ExecutionSettings{MaxModelToolSteps: 25, MaxRuntime: time.Hour, MaxConsecutiveGateFailures: 4}
	updatedDocument := `{"max_model_tool_steps": 25, "max_runtime_seconds": 3600, "max_consecutive_gate_failures": 4}`

	for _, tt := range watchStreamEnds {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b, status := newLiveApplyBoot(t, fileConfig())
			stub := &watchStub{
				boot: map[string]*pb.Resource{
					controlplane.WorkflowExecutionSettingsKind: {
						Kind: controlplane.WorkflowExecutionSettingsKind, Version: 1, ValueJson: []byte(bootedDocument),
					},
				},
				scripts: map[string][]watchScript{
					controlplane.WorkflowExecutionSettingsKind: {
						{end: tt.end},
						{resources: []*pb.Resource{{
							Kind: controlplane.WorkflowExecutionSettingsKind, Version: 2, ValueJson: []byte(updatedDocument),
						}}},
					},
				},
			}
			b.controlPlane = controlplane.NewRPCClient(stub)

			if err := b.startWorkflowExecutionSettings(t.Context()); err != nil {
				t.Fatalf("startWorkflowExecutionSettings: %v", err)
			}

			awaitRunningSettings(t, b, updated)

			// The reconnect resumes after the version this process last handled
			// rather than from zero: the second stream is asked for everything
			// after version 1, so a reconnect neither replays the document it
			// already applied over a newer one nor skips an unseen one.
			if got := stub.resumedAfter(controlplane.WorkflowExecutionSettingsKind); !slices.Equal(got, []int64{1, 1}) {
				t.Errorf("watch resumed after %v, want the boot version then the last version handled", got)
			}

			// Boot's own apply, then the update the re-established stream
			// delivered. Neither way the stream ends writes a record of its own:
			// a stream the server closed emits no update at all (controlplane
			// .Client sends one only for a Recv error), and a Recv failure
			// carries no version, which is the unreachable store the process must
			// never report as a failure of its own
			// (docs/prds/control-plane-apply-status.md, "What can be reported, and
			// what cannot").
			records := status.awaitCount(t, controlplane.WorkflowExecutionSettingsKind, 2)
			if len(records) != 2 {
				t.Fatalf("apply status = %+v, want the boot apply and the recovered one alone", records)
			}
			if records[0].AppliedVersion != 1 || records[0].Error != "" {
				t.Errorf("boot report = %+v, want a clean apply of version 1", records[0])
			}
			last := records[len(records)-1]
			if last.AppliedVersion != 2 || last.Error != "" {
				t.Errorf("last apply status = %+v, want the recovered apply of version 2 with the failure cleared", last)
			}
		})
	}
}

// TestWatchTransportFailureIsNotReportedAsAFailedApply is
// docs/prds/control-plane-apply-status.md, "What can be reported, and what
// cannot": "When the State Store is unreachable ... An unreachable store is
// never reported as a failure by the process it affected. A record carries the
// other failure: the store answered, and the value would not validate."
//
// A stream failure is that unreachable store: controlplane.Client emits it with
// no version, because a Recv error is the transport and not a document. An
// earlier revision reported it anyway, against the version still live, and the
// record then could not be cleared: a re-established stream over an unchanged
// store delivers nothing (the server sends only what is newer than the resume
// point), applystatus.Reporter keeps a standing error and re-stamps it forever,
// and the dashboard renders any non-empty error as failed. The process reads as
// failed for the rest of its life, after one blip.
func TestWatchTransportFailureIsNotReportedAsAFailedApply(t *testing.T) {
	t.Parallel()
	b, recorder := newLiveApplyBoot(t, fileConfig())
	stub := &watchStub{
		boot: map[string]*pb.Resource{
			controlplane.WorkflowExecutionSettingsKind: {
				Kind: controlplane.WorkflowExecutionSettingsKind, Version: 1, ValueJson: []byte(bootedDocument),
			},
		},
		scripts: map[string][]watchScript{
			controlplane.WorkflowExecutionSettingsKind: {
				// The store goes away, and comes back unchanged: the second
				// stream carries no document, so only a report written for the
				// failure itself could ever be cleared.
				{end: status.Error(codes.Unavailable, "state store restarting")},
				{},
			},
		},
	}
	b.controlPlane = controlplane.NewRPCClient(stub)

	if err := b.startWorkflowExecutionSettings(t.Context()); err != nil {
		t.Fatalf("startWorkflowExecutionSettings: %v", err)
	}

	// The failure has been delivered by the time the second stream is opened:
	// the loop delivers what ended the stream, then reopens.
	stub.awaitWatchCalls(t, controlplane.WorkflowExecutionSettingsKind, 2)

	records := recorder.awaitCount(t, controlplane.WorkflowExecutionSettingsKind, 1)
	if len(records) != 1 {
		t.Fatalf("apply status = %+v, want the boot apply alone: an unreachable store writes no record", records)
	}
	if last := records[0]; last.AppliedVersion != 1 || last.Error != "" {
		t.Errorf("apply status = %+v, want the clean apply of version 1 still standing", last)
	}
}

// TestWatchTransportFailureDoesNotOverwriteARefusal is the other half of the
// same rule, and the reason the branch is on the version rather than on the
// error: a refused document arrives carrying the version it came from, so it is
// the store answering and must be reported; a transport failure carries no
// version and must not. Reporting the transport failure against the version
// still live overwrites the refusal's text, so a validation error against
// version 2 reads "control-plane unavailable: ..." after any blip, and the
// operator loses the one message that says what to fix.
func TestWatchTransportFailureDoesNotOverwriteARefusal(t *testing.T) {
	t.Parallel()
	b, recorder := newLiveApplyBoot(t, fileConfig())
	stub := &watchStub{
		boot: map[string]*pb.Resource{
			controlplane.WorkflowExecutionSettingsKind: {
				Kind: controlplane.WorkflowExecutionSettingsKind, Version: 1, ValueJson: []byte(bootedDocument),
			},
		},
		scripts: map[string][]watchScript{
			controlplane.WorkflowExecutionSettingsKind: {
				{
					resources: []*pb.Resource{{
						Kind: controlplane.WorkflowExecutionSettingsKind, Version: 2,
						ValueJson: []byte(`{"max_model_tool_steps": -1, "max_runtime_seconds": 3600, "max_consecutive_gate_failures": 4}`),
					}},
					end: status.Error(codes.Unavailable, "state store restarting"),
				},
				{},
			},
		},
	}
	b.controlPlane = controlplane.NewRPCClient(stub)

	if err := b.startWorkflowExecutionSettings(t.Context()); err != nil {
		t.Fatalf("startWorkflowExecutionSettings: %v", err)
	}

	stub.awaitWatchCalls(t, controlplane.WorkflowExecutionSettingsKind, 2)

	records := recorder.awaitCount(t, controlplane.WorkflowExecutionSettingsKind, 2)
	if len(records) != 2 {
		t.Fatalf("apply status = %+v, want the boot apply and the refusal alone", records)
	}
	last := records[len(records)-1]
	if last.AppliedVersion != 1 {
		t.Errorf("refusal reported against version %d, want the version still live (1)", last.AppliedVersion)
	}
	if !strings.Contains(last.Error, "max model/tool steps must not be negative") {
		t.Errorf("apply status error = %q, want the refusal's own text", last.Error)
	}
	if strings.Contains(last.Error, "control-plane unavailable") {
		t.Errorf("apply status error = %q, want the transport blip not to have overwritten the refusal", last.Error)
	}
	// The refused version is unusable to this build, so the resume point moves
	// past it rather than letting every reconnect re-offer it forever.
	if got := stub.resumedAfter(controlplane.WorkflowExecutionSettingsKind); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("watch resumed after %v, want the boot version then the refused one", got)
	}
}

// TestWatchReconnectDoesNotReplayWhatItAlreadyApplied ties the resume contract
// to both of its ends at once. The re-established stream is asked for
// everything after the last version this process handled, and the stub -- like
// controlplane.Server.Watch -- serves only what is newer than that, so a store
// offering its whole history back is filtered down to the one version this
// process has not seen. A replay would be applied over settings this process
// already runs, and reported a second time.
func TestWatchReconnectDoesNotReplayWhatItAlreadyApplied(t *testing.T) {
	t.Parallel()
	updated := workflow.ExecutionSettings{MaxModelToolSteps: 25, MaxRuntime: time.Hour, MaxConsecutiveGateFailures: 4}
	updatedDocument := `{"max_model_tool_steps": 25, "max_runtime_seconds": 3600, "max_consecutive_gate_failures": 4}`
	b, recorder := newLiveApplyBoot(t, fileConfig())
	stub := &watchStub{
		boot: map[string]*pb.Resource{
			controlplane.WorkflowExecutionSettingsKind: {
				Kind: controlplane.WorkflowExecutionSettingsKind, Version: 1, ValueJson: []byte(bootedDocument),
			},
		},
		scripts: map[string][]watchScript{
			controlplane.WorkflowExecutionSettingsKind: {
				{end: status.Error(codes.Unavailable, "state store restarting")},
				// The version this process already ran, offered alongside the new
				// one, as a server asked from zero would offer it.
				{resources: []*pb.Resource{
					{Kind: controlplane.WorkflowExecutionSettingsKind, Version: 1, ValueJson: []byte(bootedDocument)},
					{Kind: controlplane.WorkflowExecutionSettingsKind, Version: 2, ValueJson: []byte(updatedDocument)},
				}},
			},
		},
	}
	b.controlPlane = controlplane.NewRPCClient(stub)

	if err := b.startWorkflowExecutionSettings(t.Context()); err != nil {
		t.Fatalf("startWorkflowExecutionSettings: %v", err)
	}

	awaitRunningSettings(t, b, updated)
	if got := stub.resumedAfter(controlplane.WorkflowExecutionSettingsKind); !slices.Equal(got, []int64{1, 1}) {
		t.Errorf("watch resumed after %v, want the boot version then the last version handled", got)
	}

	// One report for the boot apply and one for the update: a third would be
	// the re-served version 1 applied over itself.
	records := recorder.awaitCount(t, controlplane.WorkflowExecutionSettingsKind, 2)
	if len(records) != 2 {
		t.Fatalf("apply status = %+v, want the boot apply and the update alone", records)
	}
	last := records[len(records)-1]
	if last.AppliedVersion != 2 || last.Error != "" {
		t.Errorf("last apply status = %+v, want the recovered apply of version 2", last)
	}
}

// TestPersonaWatchIsReEstablishedAfterTheStreamEnds is the persona half of the
// same gap, over the seam the daemon meets persona edits through: the persona
// watch is launched the same way and ends the same way, so a persona edit
// stored after a stream failure must still reach the registry.
func TestPersonaWatchIsReEstablishedAfterTheStreamEnds(t *testing.T) {
	booted := `{"default":"archie","personas":[{"name":"archie","prompt":"the baseline"}]}`
	updated := `{"default":"plain","personas":[{"name":"plain","prompt":"no flourish"}]}`

	for _, tt := range watchStreamEnds {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := &boot{cfg: fileConfig(), log: slog.New(slog.DiscardHandler)}
			b.personas = gateway.NewPersonaRegistry(nil)
			stub := &watchStub{
				boot: map[string]*pb.Resource{
					controlplane.PersonasKind: {Kind: controlplane.PersonasKind, Version: 1, ValueJson: []byte(booted)},
				},
				scripts: map[string][]watchScript{
					controlplane.PersonasKind: {
						{end: tt.end},
						{resources: []*pb.Resource{{
							Kind: controlplane.PersonasKind, Version: 2, ValueJson: []byte(updated),
						}}},
					},
				},
			}
			b.controlPlane = controlplane.NewRPCClient(stub)

			if err := b.watchPersonas(t.Context(), 1); err != nil {
				t.Fatalf("watchPersonas: %v", err)
			}

			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, ok := b.personas.Get("plain"); ok {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the persona edit stored after the stream ended was never applied: personas = %v", b.personas.List())
				}
				time.Sleep(5 * time.Millisecond)
			}
			if got := b.personas.List(); !slices.Equal(got, []string{"plain"}) {
				t.Errorf("personas = %v, want the re-established stream's collection alone", got)
			}
		})
	}
}

// TestKeepWatchGuardrails pins the properties the reconnect loop must not
// trade away for recovery: one stream at a time, a retry that is spaced rather
// than spun, a shutdown that does not wait out the backoff, and a resume point
// that only updates carrying a version can move.
func TestKeepWatchGuardrails(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	versionOf := func(version int64) int64 { return version }
	t.Run("one stream is held at a time", func(t *testing.T) {
		var opens atomic.Int64
		first := make(chan int64)
		close(first)
		// Every re-established stream stays open, so a reconnect that stacked
		// watchers instead of replacing the stream would keep opening more.
		open := func(ctx context.Context, _ int64) (<-chan int64, error) {
			opens.Add(1)
			return idleStream[int64](ctx), nil
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			keepWatch(ctx, log, "test", 0, first, open, waitFor, versionOf, func(int64) {})
		}()

		awaitOpenCount(t, &opens, 1)
		time.Sleep(2 * controlPlaneWatchRetryMin)
		if got := opens.Load(); got != 1 {
			t.Errorf("streams opened = %d while one was still live, want 1: the watch reconnects in place", got)
		}
		cancel()
		awaitStopped(t, done)
	})

	t.Run("a stream that cannot be re-established is retried with backoff", func(t *testing.T) {
		var opens atomic.Int64
		first := make(chan int64)
		close(first)
		open := func(context.Context, int64) (<-chan int64, error) {
			opens.Add(1)
			return nil, errors.New("state store unreachable")
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			keepWatch(ctx, log, "test", 0, first, open, waitFor, versionOf, func(int64) {})
		}()

		firstOpen := awaitOpenCount(t, &opens, 1)
		secondOpen := awaitOpenCount(t, &opens, 2)
		thirdOpen := awaitOpenCount(t, &opens, 3)
		previous := secondOpen.Sub(firstOpen)
		if previous < controlPlaneWatchRetryMin {
			t.Errorf("retried after %v, want at least the %v minimum backoff: a failing stream must not be reopened in a loop", previous, controlPlaneWatchRetryMin)
		}
		if grew := thirdOpen.Sub(secondOpen); grew <= previous {
			t.Errorf("the retry delay went from %v to %v, want each attempt spaced further apart than the last", previous, grew)
		}
		cancel()
		awaitStopped(t, done)
	})

	t.Run("a cancelled context ends the watch while it is backing off", func(t *testing.T) {
		var opens atomic.Int64
		first := make(chan int64)
		close(first)
		open := func(context.Context, int64) (<-chan int64, error) {
			opens.Add(1)
			return nil, errors.New("state store unreachable")
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			keepWatch(ctx, log, "test", 0, first, open, waitFor, versionOf, func(int64) {})
		}()

		// One retry is in flight, so the loop is waiting out a backoff.
		awaitOpenCount(t, &opens, 1)
		start := time.Now()
		cancel()
		awaitStopped(t, done)
		if stopped := time.Since(start); stopped > controlPlaneWatchRetryMin/2 {
			t.Errorf("the watch stopped %v after the context ended, want it to stop without waiting out the backoff", stopped)
		}
	})

	t.Run("the reconnect resumes after the last version delivered", func(t *testing.T) {
		// A version this process applied, then the update a stream failure
		// arrives as: the failure carries no version, so it must not move the
		// resume point and the next stream must not be asked to replay history.
		first := make(chan int64, 2)
		first <- 7
		first <- 0
		close(first)
		resumed := make(chan int64, 1)
		open := func(ctx context.Context, afterVersion int64) (<-chan int64, error) {
			resumed <- afterVersion
			return idleStream[int64](ctx), nil
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			keepWatch(ctx, log, "test", 0, first, open, waitFor, versionOf, func(int64) {})
		}()

		select {
		case after := <-resumed:
			if after != 7 {
				t.Errorf("re-established the stream after version %d, want the last version handled (7)", after)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the stream was never re-established")
		}
		cancel()
		awaitStopped(t, done)
	})
}

// TestKeepWatchChargesAnAttemptThatEndedWithNothing pins the reconnect delay
// against the three ways one attempt can end, which is the whole of the
// decision: an attempt that delivered an update of the store's is healthy even
// though the stream ended the instant after it, an attempt that was still open
// when the window it would have been charged closed is healthy even though the
// store had nothing to say, and an attempt that did neither failed -- the
// stream was accepted and then died without the store having answered -- so the
// reconnect behind it is charged.
//
// The window case stands open past the window rather than for exactly it: the
// sleep in it starts before the loop's attempt does, so an attempt measured
// against the window it was started with is an attempt that can be judged a
// hair early. The edge itself -- the window closing exactly on the attempt's
// end -- is pinned without a clock by TestAttemptHealthyAtTheWindowBoundary.
//
// Every case reopens successfully, so a loop that reset the delay on the mere
// acceptance of a stream would report the minimum for the failed attempt too:
// gRPC takes the stream before the handler reads anything, so during a store
// outage every reopen is accepted and every stream ends carrying nothing, and a
// delay reset by that acceptance spins at the minimum -- the case this loop
// exists for. The delay is read off the watch's own log line, which is where
// the loop states the wait it is about to take.
func TestKeepWatchChargesAnAttemptThatEndedWithNothing(t *testing.T) {
	tests := []struct {
		name  string
		first func() <-chan int64
		want  time.Duration
	}{
		{
			name:  "an attempt that delivered nothing and died inside the window it would be charged failed",
			first: func() <-chan int64 { stream := make(chan int64); close(stream); return stream },
			want:  2 * controlPlaneWatchRetryMin,
		},
		{
			name:  "an attempt that carried an update is healthy however fast it ended",
			first: func() <-chan int64 { stream := make(chan int64, 1); stream <- 8; close(stream); return stream },
			want:  controlPlaneWatchRetryMin,
		},
		{
			name: "an attempt that outlived the window it would be charged is healthy",
			first: func() <-chan int64 {
				stream := make(chan int64)
				go func() {
					defer close(stream)
					time.Sleep(controlPlaneWatchHealthyWindow(controlPlaneWatchRetryMin) + controlPlaneWatchRetryMin/10)
				}()
				return stream
			},
			want: controlPlaneWatchRetryMin,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			log, retries := newRetryLog()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			open := func(ctx context.Context, _ int64) (<-chan int64, error) { return idleStream[int64](ctx), nil }
			go keepWatch(ctx, log, "test", 0, tt.first(), open, waitFor, func(version int64) int64 { return version }, func(int64) {})

			if got := awaitRetries(t, retries, 1)[0]; got != tt.want {
				t.Errorf("the reconnect waited %v, want %v: %s", got, tt.want, tt.name)
			}
			cancel()
		})
	}
}

// TestKeepWatchTreatsAStallPastTheWindowAsAStoreThatAnswered pins the outer
// boundary of attemptHealthy where it is decided, and documents what the rule
// stops reaching: the derived window charges a stall shorter than itself and
// stops there. A stall still going when the window closes ends an attempt that
// was open for the whole window, so the rule reads it as a store that answered
// and had nothing to say, and the ladder is left at the minimum -- [250ms
// 250ms], not [250ms 500ms].
//
// That is deliberate, and it is not a gap to close with a further rule. The
// stalled attempt itself lasts the window, so the reopens it leaves behind are
// under one a second at the minimum rung, against the four a second this rule
// exists to charge, and a shape that slow is not the request loop. What the
// boundary does mean is that an outage is charged by its failed attempts only
// while they fail faster than the window; one whose every attempt takes that
// long is reconnected at the minimum, and its RPC rate is bounded by the
// attempt rather than by the delay.
func TestKeepWatchTreatsAStallPastTheWindowAsAStoreThatAnswered(t *testing.T) {
	t.Parallel()
	log, retries := newRetryLog()
	// The minimum rung judges an attempt against a window of four times the
	// minimum (controlPlaneWatchHealthyWindow(controlPlaneWatchRetryMin)), and
	// each attempt here stands open for five times the minimum -- a whole
	// minimum past that window -- carrying nothing: a store that has stopped
	// answering and never errors. The margin is a whole minimum rather than a
	// hair, because the sleep starts before the loop's attempt does and a race
	// with the window is not what this test is for.
	stall := 5 * controlPlaneWatchRetryMin
	stalled := func() <-chan int64 {
		stream := make(chan int64)
		go func() {
			defer close(stream)
			time.Sleep(stall)
		}()
		return stream
	}
	open := func(context.Context, int64) (<-chan int64, error) { return stalled(), nil }
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go keepWatch(ctx, log, "test", 0, stalled(), open, waitFor, func(version int64) int64 { return version }, func(int64) {})

	want := []time.Duration{controlPlaneWatchRetryMin, controlPlaneWatchRetryMin}
	if got := awaitRetries(t, retries, len(want)); !slices.Equal(got[:len(want)], want) {
		t.Errorf("reconnects waited %v, want %v: an attempt that stayed open for the whole %v window is read as a store that answered, so the ladder is left at the minimum and the reopen rate is bounded by the attempt itself rather than by the delay",
			got[:len(want)], want, controlPlaneWatchHealthyWindow(controlPlaneWatchRetryMin))
	}
	cancel()
}

// TestAttemptHealthyAtTheWindowBoundary pins the edge where the sentence and
// the operator meet, which nothing else in the package does: the comparison is
// inclusive, so an attempt still open when the window closes -- at it, and not
// only past it -- is a store that answered, and an attempt a nanosecond inside
// it is charged. The window is a strictly wider multiple of the delay the
// attempt would otherwise be charged at every rung of the ladder, the cap
// included, so a store that fails as slowly as the charge it would be paid is
// still charged: an inclusive boundary does not read that store as healthy.
func TestAttemptHealthyAtTheWindowBoundary(t *testing.T) {
	t.Parallel()
	for _, backoff := range []time.Duration{controlPlaneWatchRetryMin, 3 * time.Second, controlPlaneWatchRetryMax} {
		t.Run(backoff.String(), func(t *testing.T) {
			t.Parallel()
			charge := retryDelay(backoff, false)
			window := controlPlaneWatchHealthyWindow(backoff)
			if window <= charge {
				t.Fatalf("the window at a %v charge is %v, want strictly wider than the charge: a window equal to it reads a store that fails as slowly as the charge as healthy on every attempt, which is the fixed-rate loop the window exists to charge", charge, window)
			}
			for _, tt := range []struct {
				name    string
				openFor time.Duration
				want    bool
			}{
				{"an attempt still open when the window closes is healthy", window, true},
				{"an attempt that died a nanosecond inside the window is charged", window - time.Nanosecond, false},
			} {
				if got := attemptHealthy(false, tt.openFor, backoff); got != tt.want {
					t.Errorf("attemptHealthy(progressed=false, openFor=%v, backoff=%v) = %t, want %t: %s", tt.openFor, backoff, got, tt.want, tt.name)
				}
			}
		})
	}
}

// TestKeepWatchBackoffResetsAfterASuccessfulReopen pins the retry delay's other
// half: it grows while attempts fail, and a reopen the store answers returns it
// to the minimum. Without the reset a store that is up and quiet ratchets the
// delay up to the cap -- it is only ever reset by a healthy attempt, and a quiet
// store sends nothing -- and the next outage is charged the whole of it.
//
// Successful means the store answered the reopened stream, which is what the
// window is for and not what the acceptance of a stream is: gRPC takes the
// stream before the handler reads anything, so a store that is down accepts
// every reopen. TestKeepWatchChargesAReopenTheStoreMerelyAccepted is the other
// half of that, and it is the half this test cannot pin: it reads a delay the
// log says was the minimum, and the minimum is also what a loop that reset on
// the acceptance would leave behind. The delay is read off the watch's own log
// line, which is where the loop states the wait it is about to take.
//
// The re-established stream is quiet for longer than the window that attempt
// would be charged with before this test ends it, which is what makes it
// healthy: a store that answers and then says nothing for a while is a store
// that is there. The first stream is the failed attempt at the other end of the
// same rule -- it ended at once having delivered nothing -- so the delay has
// something to reset from.
func TestKeepWatchBackoffResetsAfterASuccessfulReopen(t *testing.T) {
	log, retries := newRetryLog()
	first := make(chan int64)
	close(first)
	// The store is up and quiet once the first reopen succeeds: the stream stays
	// open until this test ends it, so no update arrives to reset the delay.
	quiet := make(chan int64)
	var opens atomic.Int64
	open := func(context.Context, int64) (<-chan int64, error) {
		opens.Add(1)
		return quiet, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		keepWatch(ctx, log, "test", 0, first, open, waitFor, func(version int64) int64 { return version }, func(int64) {})
	}()

	awaitOpenCount(t, &opens, 1)
	// Quiet for longer than the window this attempt would be charged with, so
	// this attempt is the healthy one. The next stream end is the outage that
	// follows it.
	time.Sleep(controlPlaneWatchHealthyWindow(2 * controlPlaneWatchRetryMin))
	close(quiet)
	awaitOpenCount(t, &opens, 2)

	delays := awaitRetries(t, retries, 2)
	if delays[0] != 2*controlPlaneWatchRetryMin {
		t.Errorf("the retry after an attempt that ended at once waited %v, want %v: it delivered nothing", delays[0], 2*controlPlaneWatchRetryMin)
	}
	if delays[1] != controlPlaneWatchRetryMin {
		t.Errorf("the retry after a reopened stream the store answered waited %v, want %v: the store answered, so the outage was not charged the whole of it", delays[1], controlPlaneWatchRetryMin)
	}
	cancel()
	awaitStopped(t, done)
}

// TestKeepWatchChargesAReopenTheStoreMerelyAccepted pins the unit the delay is
// decided on. The acceptance of a reopen is not it: a control plane takes the
// stream before the handler reads anything, so a store that is down accepts
// every reopen and every one of those streams then ends at once carrying
// nothing. A loop that reset the delay there -- the shape this loop was written
// to remove -- charges each attempt of the outage from the minimum and never
// climbs, which is retry_in flat at one charge per attempt while every reopen
// succeeded. These attempts are charged instead, one rung each, from the
// minimum they start at. The delay is read off the watch's own log line, which
// is where the loop states the wait it is about to take.
func TestKeepWatchChargesAReopenTheStoreMerelyAccepted(t *testing.T) {
	log, retries := newRetryLog()
	first := make(chan int64)
	close(first)
	// Accepted, over the moment it is read, and carrying nothing: what a reopen
	// against a store that is down hands back, however many times it is asked.
	flaky := make(chan int64)
	close(flaky)
	var opens atomic.Int64
	open := func(context.Context, int64) (<-chan int64, error) {
		opens.Add(1)
		return flaky, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		keepWatch(ctx, log, "test", 0, first, open, waitFor, func(version int64) int64 { return version }, func(int64) {})
	}()

	want := []time.Duration{2 * controlPlaneWatchRetryMin, 4 * controlPlaneWatchRetryMin, 8 * controlPlaneWatchRetryMin}
	delays := awaitRetries(t, retries, len(want))
	if got := delays[:len(want)]; !slices.Equal(got, want) {
		t.Errorf("reconnects waited %v, want %v: every one of those reopens was accepted and the attempt that carried it delivered nothing, so each is charged the delay before it doubled rather than reset by the acceptance", got, want)
	}
	cancel()
	awaitStopped(t, done)
}

// TestKeepWatchWaitsTheDelayItCharged is the behavioural half of the ladder.
// The retry_in attribute states the delay the loop is about to take, so a loop
// that waited some other multiple of it -- twice what it logged, say -- would
// report a growing ladder to the operator and reopen on a schedule of its own,
// and the attribute agrees with itself whatever the loop waits. The wait is
// injected, so what the loop waited is read back as values and asserted against
// the ladder it charged: the same ladder TestReconnectDelayFollowsAttemptHealth
// pins for retryDelay, driven through the loop instead of the function.
func TestKeepWatchWaitsTheDelayItCharged(t *testing.T) {
	log, retries := newRetryLog()
	recorder := &waitRecorder{}
	first := make(chan int64)
	close(first)
	// The store is gone: every reopen is refused, so nothing but the delay the
	// loop charges decides when the next attempt starts.
	open := func(context.Context, int64) (<-chan int64, error) {
		return nil, errors.New("state store unreachable")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		keepWatch(ctx, log, "test", 0, first, open, recorder.wait, func(version int64) int64 { return version }, func(int64) {})
	}()

	want := []time.Duration{
		2 * controlPlaneWatchRetryMin, 4 * controlPlaneWatchRetryMin,
		8 * controlPlaneWatchRetryMin, 16 * controlPlaneWatchRetryMin,
	}
	if waited := recorder.await(t, len(want)); !slices.Equal(waited, want) {
		t.Errorf("the watch waited %v, want %v: the delay it waits is the delay the attempt that just ended charged it", waited, want)
	}
	// What the operator reads has to be the delay the loop took, and not a
	// figure of its own.
	if logged := awaitRetries(t, retries, len(want)); !slices.Equal(logged[:len(want)], want) {
		t.Errorf("the watch logged retries %v, want %v: retry_in is the delay the loop is about to wait", logged[:len(want)], want)
	}
	cancel()
	awaitStopped(t, done)
}

// TestReconnectDelayFollowsAttemptHealth pins the whole of the ladder the
// reconnect climbs, which no test of the loop can reach in reasonable time: the
// delay doubles on every failed attempt, stops at the cap instead of doubling
// for as long as the outage lasts, and returns to the minimum the moment an
// attempt is healthy. Attempt health itself is pinned at the loop, where it is
// read off the stream (TestKeepWatchChargesAnAttemptThatEndedWithNothing).
func TestReconnectDelayFollowsAttemptHealth(t *testing.T) {
	tests := []struct {
		name      string
		healthy   []bool
		wantWaits []time.Duration
	}{
		{
			name:    "consecutive failed attempts double the delay and then hold at the cap",
			healthy: []bool{false, false, false, false, false, false, false, false, false, false},
			wantWaits: []time.Duration{
				500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
				16 * time.Second, controlPlaneWatchRetryMax, controlPlaneWatchRetryMax, controlPlaneWatchRetryMax, controlPlaneWatchRetryMax,
			},
		},
		{
			name:      "a healthy attempt returns the delay to the minimum at once",
			healthy:   []bool{false, false, false, true, false},
			wantWaits: []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, controlPlaneWatchRetryMin, 500 * time.Millisecond},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			delay := controlPlaneWatchRetryMin
			waits := make([]time.Duration, 0, len(tt.healthy))
			for _, healthy := range tt.healthy {
				delay = retryDelay(delay, healthy)
				waits = append(waits, delay)
			}
			if !slices.Equal(waits, tt.wantWaits) {
				t.Errorf("retry waits = %v, want %v: %s", waits, tt.wantWaits, tt.name)
			}
		})
	}
}

// TestKeepWatchResumesPastAVersionedFailure pins the resume point's behaviour
// on an update that carries a version and an error at once, which is how
// controlplane.Client delivers a document this process refuses to decode: the
// store answered, so the update carries the version it answered with. The
// resume point moves past it deliberately -- that version is unusable to this
// build, and resuming before it would re-offer it on every reconnect for the
// life of the process -- so the next reader must not "fix" this into a hot
// loop.
func TestKeepWatchResumesPastAVersionedFailure(t *testing.T) {
	type refusal struct {
		version int64
		err     error
	}
	first := make(chan refusal, 2)
	first <- refusal{version: 7}
	first <- refusal{version: 8, err: errors.New("max model/tool steps must not be negative")}
	close(first)
	resumed := make(chan int64, 1)
	open := func(ctx context.Context, afterVersion int64) (<-chan refusal, error) {
		resumed <- afterVersion
		return idleStream[refusal](ctx), nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		keepWatch(ctx, slog.New(slog.DiscardHandler), "test", 0, first, open, waitFor,
			func(update refusal) int64 { return update.version }, func(refusal) {})
	}()

	select {
	case after := <-resumed:
		if after != 8 {
			t.Errorf("re-established the stream after version %d, want the refused version's own (8)", after)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream was never re-established")
	}
	cancel()
	awaitStopped(t, done)
}

// TestKeepWatchReturnsOnCancellationWithANeverClosingStream is the shutdown
// half of the loop. The range over the stream returns today only because
// controlplane.Client's producer closes its channel when the stream's context
// ends, so shutdown promptness rests on an untested cross-package coupling; a
// stream that never closes would otherwise hold the process up past the
// cancellation that asked it to stop, however long its backoff cap is.
func TestKeepWatchReturnsOnCancellationWithANeverClosingStream(t *testing.T) {
	stream := make(chan int64)
	open := func(context.Context, int64) (<-chan int64, error) {
		t.Error("the stream never ended, so nothing should have been reopened")
		return nil, errors.New("not reopened")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		keepWatch(ctx, slog.New(slog.DiscardHandler), "test", 0, stream, open, waitFor,
			func(version int64) int64 { return version }, func(int64) {})
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the watch was still blocked on a stream that never closed 2s after its context ended")
	}
}

// TestKeepWatchLeavesNoProducerParkedOnAnUndeliveredUpdate is the producer half
// of the shutdown coupling nextUpdate names. The watch returns on cancellation
// without draining what the producer has left for it, so the producer must stop
// on the same context rather than park on a send no reader will ever take. A
// bare send does park: reproduced 40/40 times as a goroutine still blocked on an
// undelivered send after keepWatch returned, one goroutine and one channel
// leaked per kind at every exit.
func TestKeepWatchLeavesNoProducerParkedOnAnUndeliveredUpdate(t *testing.T) {
	t.Run("workflow execution settings", func(t *testing.T) {
		t.Parallel()
		client := controlplane.NewRPCClient(busyWatch{resource: &pb.Resource{
			Kind: controlplane.WorkflowExecutionSettingsKind, Version: 2, ValueJson: []byte(bootedDocument),
		}})
		assertProducerStopsWhenTheReadingStops(t, client.WatchWorkflowExecutionSettings,
			func(update controlplane.AppliedSettings) int64 { return update.Version },
			func(update controlplane.AppliedSettings) {
				if update.Err != nil {
					t.Errorf("the watch was handed %v, want the document the store answers with", update.Err)
				}
			})
	})
	t.Run("personas", func(t *testing.T) {
		t.Parallel()
		client := controlplane.NewRPCClient(busyWatch{resource: &pb.Resource{
			Kind: controlplane.PersonasKind, Version: 2,
			ValueJson: []byte(`{"default":"archie","personas":[{"name":"archie","prompt":"the baseline"}]}`),
		}})
		assertProducerStopsWhenTheReadingStops(t, client.WatchPersonas,
			func(update controlplane.AppliedPersonas) int64 { return update.Version },
			func(update controlplane.AppliedPersonas) {
				if update.Err != nil {
					t.Errorf("the watch was handed %v, want the document the store answers with", update.Err)
				}
			})
	})
}

// assertProducerStopsWhenTheReadingStops runs the watch against a producer that
// always has another document ready, stops reading it, and then holds the
// channel the producer writes to. An update that arrives once the watch has
// returned is a send the producer was parked on.
func assertProducerStopsWhenTheReadingStops[T any](
	t *testing.T,
	watch func(ctx context.Context, afterVersion int64) (<-chan T, error),
	versionOf func(update T) int64,
	deliver func(update T),
) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	updates, err := watch(ctx, 1)
	if err != nil {
		t.Fatalf("watching: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		keepWatch(ctx, slog.New(slog.DiscardHandler), "test", 1, updates, watch, waitFor, versionOf, deliver)
	}()

	// A few rounds through Recv and a send, then the cancellation the watch
	// answers by returning without draining what is left behind it.
	time.Sleep(25 * time.Millisecond)
	cancel()
	awaitStopped(t, done)

	// The producer's select has only the context left to choose from now that
	// the watch has gone, and this wait is what lets it take that case: a
	// receive here would be the reader that unblocks a parked send, and would
	// hide the leak as a delivered update.
	time.Sleep(250 * time.Millisecond)
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case update, ok := <-updates:
			if !ok {
				return
			}
			t.Fatalf("the producer handed the watch %v after the watch had returned: it was parked on a send no reader will take, leaking a goroutine and a channel per kind at every exit", update)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the producer never closed the watch's stream after the watch returned")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// busyWatch is the rpc end of a watch that always has another document of one
// kind ready: Recv never blocks and never ends, so the client's producer is
// only ever waiting on its reader. That is the wait a watch that stops reading
// leaves it in, and the one a bare send parks on for the life of the process.
type busyWatch struct{ resource *pb.Resource }

func (busyWatch) Catalog(context.Context, *pb.CatalogRequest, ...grpc.CallOption) (*pb.CatalogResponse, error) {
	return &pb.CatalogResponse{}, nil
}

func (b busyWatch) Query(context.Context, *pb.QueryRequest, ...grpc.CallOption) (*pb.QueryResponse, error) {
	return &pb.QueryResponse{Resource: b.resource}, nil
}

func (busyWatch) History(context.Context, *pb.HistoryRequest, ...grpc.CallOption) (*pb.HistoryResponse, error) {
	panic("unexpected History")
}

func (busyWatch) Command(context.Context, *pb.CommandRequest, ...grpc.CallOption) (*pb.CommandResponse, error) {
	panic("unexpected Command")
}

func (b busyWatch) Watch(context.Context, *pb.WatchRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[pb.WatchResponse], error) {
	return &busyStream{resource: b.resource}, nil
}

// busyStream is the client end of that stream.
type busyStream struct {
	grpc.ClientStream
	resource *pb.Resource
}

func (s *busyStream) Recv() (*pb.WatchResponse, error) {
	return &pb.WatchResponse{Resource: s.resource}, nil
}

// keepWatchWaitArgument is the position of keepWatch's wait parameter
// (ctx, log, kind, after, first, open, wait, versionOf, deliver): the injection
// point that makes the ladder observable, and so an argument whose value a call
// site could get wrong with no behavioural test behind it.
const keepWatchWaitArgument = 6

// TestBothWatchStreamsRunThroughTheReconnectLoop pins the wiring the
// behavioural tests drive one step below: both launch sites must hand their
// stream to the one reconnect loop, or a kind keeps the single-shot goroutine
// that ends for the life of the process. What the loop is handed is pinned too,
// because a call site that reached keepWatch with a wait of its own would be a
// second schedule nothing else drives: the persona launch site's wait has no
// behavioural test of its own -- the outage test reads the delays one level
// down, and only for the settings launch site. Asserted on the source because
// setupChatRuntime needs a fully built boot (stores, runtime, memory engines)
// to run -- the technique composition_order_test.go and
// TestSetupGatewayChatCarriesStatusHealth already use for wiring nothing at
// runtime observes.
func TestBothWatchStreamsRunThroughTheReconnectLoop(t *testing.T) {
	fileset := token.NewFileSet()
	for _, tt := range []struct {
		file, holder, call string
		// wait is the name the call must hand keepWatch in its wait position,
		// empty for a call that is not keepWatch itself.
		wait string
	}{
		{file: "control_plane.go", holder: "startWorkflowExecutionSettings", call: "keepWatch", wait: "waitFor"},
		{file: "gateway_runtime.go", holder: "setupChatRuntime", call: "watchPersonas"},
		{file: "gateway_runtime.go", holder: "watchPersonas", call: "keepWatch", wait: "waitFor"},
	} {
		parsed, err := parser.ParseFile(fileset, tt.file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", tt.file, err)
		}
		call := callTo(methodBody(t, parsed, tt.holder), tt.call)
		if call == nil {
			t.Errorf("%s never calls %s: that watch stream would not be re-established", tt.holder, tt.call)
			continue
		}
		if tt.wait == "" {
			continue
		}
		if len(call.Args) <= keepWatchWaitArgument {
			t.Errorf("%s calls %s with %d arguments, want at least %d: the wait cannot be read, so nothing here pins which wait the loop is handed", tt.holder, tt.call, len(call.Args), keepWatchWaitArgument+1)
			continue
		}
		if got := types.ExprString(call.Args[keepWatchWaitArgument]); got != tt.wait {
			t.Errorf("%s hands %s %s as its wait, want %s: the loop's backoff has to be the wait that reports a cancelled context and is the injection point the ladder is read through, not a schedule of the call site's own", tt.holder, tt.call, got, tt.wait)
		}
	}
}

// callTo returns body's first call to name, as a plain function or as a method
// of anything, or nil when body does not call it.
func callTo(body *ast.BlockStmt, name string) *ast.CallExpr {
	var found *ast.CallExpr
	ast.Inspect(body, func(node ast.Node) bool {
		if found != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if fun.Name == name {
				found = call
			}
		case *ast.SelectorExpr:
			if fun.Sel.Name == name {
				found = call
			}
		}
		return true
	})
	return found
}

// retryLog is a slog.Handler that records the retry delay of each reconnect,
// and when it was logged. The delay is the reconnect loop's own state, and the
// log line is where it is observable without reading a clock.
type retryLog struct {
	mu      sync.Mutex
	retries []time.Duration
	at      []time.Time
}

func newRetryLog() (*slog.Logger, *retryLog) {
	handler := &retryLog{}
	return slog.New(handler), handler
}

func (*retryLog) Enabled(context.Context, slog.Level) bool { return true }
func (h *retryLog) WithAttrs([]slog.Attr) slog.Handler     { return h }
func (h *retryLog) WithGroup(string) slog.Handler          { return h }

func (h *retryLog) Handle(_ context.Context, record slog.Record) error {
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "retry_in" {
			h.mu.Lock()
			h.retries = append(h.retries, attr.Value.Duration())
			h.at = append(h.at, time.Now())
			h.mu.Unlock()
		}
		return true
	})
	return nil
}

// gap is how long passed between the retry logged at i and the one after it.
// The loop logs the delay it has charged and then waits it, so the gap is that
// wait plus the attempt it led to, and it can only be longer than the delay it
// followed. It is what makes the delay actually waited observable: retry_in
// states the intent, and a loop that waited some other multiple of it would
// never be caught by the attribute alone.
func (h *retryLog) gap(i int) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.at[i+1].Sub(h.at[i])
}

func (h *retryLog) snapshot() []time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.retries)
}

// waitRecorder is the wait a test injects into keepWatch: it records each delay
// and reports that it passed at once, so the ladder the loop waited is a value a
// test asserts instead of a clock it reads, and climbing the ladder costs no
// time. It answers for the delay's purpose and not for a stub of it -- true is
// "the delay passed" -- so it still reports the cancellation waitFor reports,
// which is what returns the watch a finished test is holding.
type waitRecorder struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (r *waitRecorder) wait(ctx context.Context, d time.Duration) bool {
	r.mu.Lock()
	r.waits = append(r.waits, d)
	r.mu.Unlock()
	return ctx.Err() == nil
}

// await waits until n delays have been recorded and returns them.
func (r *waitRecorder) await(t *testing.T, n int) []time.Duration {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		recorded := slices.Clone(r.waits)
		r.mu.Unlock()
		if len(recorded) >= n {
			return recorded[:n]
		}
		if time.Now().After(deadline) {
			t.Fatalf("the watch waited %d times, want %d", len(recorded), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// awaitRetries waits until the watch has logged n retries, returning what it
// logged. The retry delay is the reconnect loop's own state, and its log line
// is where it is observable without reading a clock: the loop is back in a wait
// by then, so a test can cancel it without racing the attempt.
func awaitRetries(t *testing.T, retries *retryLog, n int) []time.Duration {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if got := retries.snapshot(); len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("the watch logged %d retries, want %d", len(retries.snapshot()), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// idleStream is the client end of a stream that sends nothing: its channel
// stays open until ctx ends, the way the channel controlplane.Client returns
// closes when the stream's context ends. That is what lets a watch be
// cancelled while it is idle rather than blocked on a server that has gone
// quiet.
func idleStream[T any](ctx context.Context) <-chan T {
	stream := make(chan T)
	go func() {
		defer close(stream)
		<-ctx.Done()
	}()
	return stream
}

// awaitOpenCount waits until at least n streams have been opened, returning
// when the nth was seen. A test synchronises on the stream count because the
// reconnect loop is what opens them.
func awaitOpenCount(t *testing.T, opens *atomic.Int64, n int64) time.Time {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for opens.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("streams opened = %d, want at least %d", opens.Load(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return time.Now()
}

// awaitStopped waits for the watch goroutine to return.
func awaitStopped(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the watch goroutine did not return")
	}
}

// awaitRunningSettings waits for the watch goroutine to apply want, which is
// the only evidence that it re-established the stream: the watch runs on its
// own goroutine, so a test driving it through the stream has nothing else to
// synchronise on. The snapshot every new task is built from has to move with
// the settings, so it is read too.
func awaitRunningSettings(t *testing.T, b *boot, want workflow.ExecutionSettings) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if running := b.executionSettings.Load(); running != nil && *running == want {
			if budgets := b.cfgHolder.Get().Budgets; budgets != budgetsFor(want) {
				t.Fatalf("running budgets = %+v, want %+v", budgets, budgetsFor(want))
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the watch did not re-establish the stream, so the stored update was never applied: running settings = %v, want %+v",
				b.executionSettings.Load(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
