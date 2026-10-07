package gatewayrpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"

	pb "github.com/samcharles93/archie-core/internal/contracts/gateway/v1"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// Answers to the asks a turn is blocked on, keyed by ask id. A channel's
// requesters live in the caller's process, so a turn asks over its stream and
// the caller answers through the Answer RPC.
type pendingAsks struct {
	mu      sync.Mutex
	waiting map[string]chan string
}

func (p *pendingAsks) open() (string, chan string) {
	var b [12]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	ch := make(chan string, 1)
	p.mu.Lock()
	if p.waiting == nil {
		p.waiting = make(map[string]chan string)
	}
	p.waiting[id] = ch
	p.mu.Unlock()
	return id, ch
}

func (p *pendingAsks) close(id string) {
	p.mu.Lock()
	delete(p.waiting, id)
	p.mu.Unlock()
}

func (p *pendingAsks) answer(id, answer string) bool {
	p.mu.Lock()
	ch, ok := p.waiting[id]
	p.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- answer:
	default:
	}
	return true
}

// remoteAsker poses a turn's questions on its stream. send is serialized with
// the turn's own frames by the caller.
type remoteAsker struct {
	pending *pendingAsks
	send    func(*pb.StreamResponse) error
}

func (a remoteAsker) ask(ctx context.Context, q *pb.Ask) (string, error) {
	id, ch := a.pending.open()
	defer a.pending.close(id)
	q.Id = id
	if err := a.send(&pb.StreamResponse{Kind: "ask", Ask: q}); err != nil {
		return "", err
	}
	select {
	case answer := <-ch:
		return answer, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (a remoteAsker) RequestApproval(ctx context.Context, action, description string) (messaging.ApprovalDecision, error) {
	answer, err := a.ask(ctx, &pb.Ask{Kind: "approval", Prompt: action, Detail: description})
	if err != nil {
		return messaging.ApprovalDenied, err
	}
	switch answer {
	case "approve":
		return messaging.ApprovalApproved, nil
	case "always":
		return messaging.ApprovalPermanentlyApproved, nil
	default:
		return messaging.ApprovalDenied, nil
	}
}

func (a remoteAsker) RequestClarification(ctx context.Context, req messaging.ClarifyRequest) (string, error) {
	answer, err := a.ask(ctx, &pb.Ask{Kind: "clarify", Prompt: req.Question, Choices: choicesProto(req.Suggestions)})
	if err != nil {
		return "", err
	}
	if answer = strings.TrimSpace(answer); answer == "" {
		return "", messaging.ErrEmptyReply
	}
	return answer, nil
}

func (a remoteAsker) RequestChoice(ctx context.Context, req messaging.PickerRequest) (messaging.InteractiveChoice, error) {
	if len(req.Options) == 0 {
		return messaging.InteractiveChoice{}, messaging.ErrNoPickerOptions
	}
	answer, err := a.ask(ctx, &pb.Ask{Kind: "picker", Prompt: req.Prompt, Choices: choicesProto(req.Options)})
	if err != nil {
		return messaging.InteractiveChoice{}, err
	}
	for _, option := range req.Options {
		if option.ID == answer {
			return option, nil
		}
	}
	return messaging.InteractiveChoice{}, errors.New("picker answer matches no option")
}

func choicesProto(choices []messaging.InteractiveChoice) []*pb.Choice {
	out := make([]*pb.Choice, 0, len(choices))
	for _, c := range choices {
		out = append(out, &pb.Choice{Id: c.ID, Label: c.Label})
	}
	return out
}

// withAsks puts on ctx the requesters the caller said it can answer.
func withAsks(ctx context.Context, r *pb.StreamRequest, asker remoteAsker) context.Context {
	if r.CanApprove {
		ctx = messaging.WithApprovalRequester(ctx, asker)
	}
	var interactive messaging.Interactive
	if r.CanClarify {
		interactive.Clarifier = asker
	}
	if r.CanPick {
		interactive.Picker = asker
	}
	if interactive.Carries() {
		ctx = messaging.WithInteractive(ctx, interactive)
	}
	return ctx
}
