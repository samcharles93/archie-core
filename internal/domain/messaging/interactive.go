package messaging

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// InteractiveChoice is one selectable option offered by a clarify or picker
// prompt. ID is the value carried back to the caller; Label is what the
// human sees. An adapter that renders a choice must return the option as it
// was given (ID preserved), never a Label the human typed: callers correlate
// on ID.
type InteractiveChoice struct {
	ID    string
	Label string
}

// ClarifyRequest asks a human a question the agent cannot answer itself.
// Suggestions are optional quick replies; a channel with no button UI may
// render them as hint text and accept a typed answer instead.
type ClarifyRequest struct {
	Question    string
	Suggestions []InteractiveChoice
}

// PickerRequest asks a human to select exactly one of Options.
type PickerRequest struct {
	Prompt  string
	Options []InteractiveChoice
}

// ClarifyRequester is the optional interface an adapter implements to pose a
// clarifying question and block for the human's answer. It is the interactive
// counterpart of ApprovalRequester: an adapter that cannot render a native
// question UI fulfils it with the text fallback rather than failing the turn.
type ClarifyRequester interface {
	// RequestClarification shows req and returns the human's free-text
	// answer. The returned string is the trimmed answer, never empty when
	// err is nil.
	RequestClarification(ctx context.Context, req ClarifyRequest) (string, error)
}

// PickerRequester is the optional interface an adapter implements to present
// a single-choice selection and block for the human's chosen option.
type PickerRequester interface {
	// RequestChoice shows req and returns the option the human picked. A
	// request with no options cannot be answered and returns
	// ErrNoPickerOptions rather than an empty choice.
	RequestChoice(ctx context.Context, req PickerRequest) (InteractiveChoice, error)
}

// Interactive sentinel errors, matched with errors.Is.
var (
	// ErrNoPickerOptions is returned when a picker is asked to present an
	// empty option set: there is nothing to select, so the caller must
	// decide what to do (fall back, or ask a different question) rather
	// than receive a zero InteractiveChoice that looks like a valid answer.
	ErrNoPickerOptions = errors.New("interactive: picker has no options")

	// ErrEmptyReply is returned when a human submits no answer. An empty
	// string is not an answer to a question, so it must not be mistaken
	// for one by a caller that only checks err.
	ErrEmptyReply = errors.New("interactive: human reply was empty")

	// ErrTextFallbackNotConfigured is returned when a TextFallback is used
	// without both transport halves. It is a construction defect, surfaced
	// rather than panicking inside a turn.
	ErrTextFallbackNotConfigured = errors.New("interactive: text fallback is missing Send or Reply")
)

// ClarifierOf returns sender's ClarifyRequester only when sender reports the
// Clarify capability. A sender that implements the interface but reports
// Clarify:false is treated as unable to clarify -- the capability is the
// contract, not the method set -- and one that reports Clarify:true without
// implementing the interface is treated the same way, so a mis-reporting
// adapter degrades instead of panicking. It mirrors DeleterOf.
func ClarifierOf(sender any) (ClarifyRequester, bool) {
	if !CapabilitiesOf(sender).Clarify {
		return nil, false
	}
	clarifier, ok := sender.(ClarifyRequester)
	return clarifier, ok
}

// PickerOf returns sender's PickerRequester only when sender reports the
// Picker capability, with the same capability-over-method-set rule as
// ClarifierOf.
func PickerOf(sender any) (PickerRequester, bool) {
	if !CapabilitiesOf(sender).Picker {
		return nil, false
	}
	picker, ok := sender.(PickerRequester)
	return picker, ok
}

// FormatClarifyText renders a ClarifyRequest as a plain-text prompt for a
// channel with no native question UI.
func FormatClarifyText(req ClarifyRequest) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(req.Question))
	if len(req.Suggestions) > 0 {
		b.WriteString("\n\nSuggestions:")
		for i, s := range req.Suggestions {
			fmt.Fprintf(&b, "\n%d) %s", i+1, s.Label)
		}
	}
	b.WriteString("\n\nReply with your answer.")
	return b.String()
}

// FormatPickerText renders a PickerRequest as a numbered plain-text prompt
// for a channel with no native selection UI.
func FormatPickerText(req PickerRequest) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(req.Prompt))
	if len(req.Options) == 0 {
		b.WriteString("\n\nNo options were offered.")
		return b.String()
	}
	b.WriteString("\n")
	for i, o := range req.Options {
		fmt.Fprintf(&b, "\n%d) %s", i+1, o.Label)
	}
	b.WriteString("\n\nReply with the number of your choice.")
	return b.String()
}

// ParsePickerReply maps a human's typed reply to one of req's options. It
// accepts a 1-based index or an exact ID or Label, case-insensitively.
// ok is false when the reply matches nothing, so the caller re-prompts rather
// than guessing an option the human did not choose.
func ParsePickerReply(req PickerRequest, reply string) (InteractiveChoice, bool) {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return InteractiveChoice{}, false
	}
	if n, err := strconv.Atoi(trimmed); err == nil {
		if n >= 1 && n <= len(req.Options) {
			return req.Options[n-1], true
		}
		// A numeric reply is an index and nothing else: a label that
		// happens to look like a number must not rescue an out-of-range
		// selection.
		return InteractiveChoice{}, false
	}
	for _, opt := range req.Options {
		if strings.EqualFold(opt.ID, trimmed) || strings.EqualFold(opt.Label, trimmed) {
			return opt, true
		}
	}
	return InteractiveChoice{}, false
}

// TextFallback renders clarify and picker prompts as plain text and reads the
// human's typed reply. It is the degrade path for a channel with no native
// question or selection UI, and the behaviour an adapter claims when it
// reports Clarify/Picker without native widgets: the interaction still
// happens, it just costs a typed answer instead of a tap.
//
// Send delivers one prompt; Reply blocks for the next human text. Both are
// injected so the fallback carries no transport of its own.
type TextFallback struct {
	Send  func(ctx context.Context, text string) error
	Reply func(ctx context.Context) (string, error)
}

// Compile-time guards.
var (
	_ ClarifyRequester = (*TextFallback)(nil)
	_ PickerRequester  = (*TextFallback)(nil)
)

// RequestClarification sends the rendered question and returns the human's
// trimmed answer.
func (f *TextFallback) RequestClarification(ctx context.Context, req ClarifyRequest) (string, error) {
	if err := f.ready(); err != nil {
		return "", err
	}
	if err := f.Send(ctx, FormatClarifyText(req)); err != nil {
		return "", err
	}
	answer, err := f.Reply(ctx)
	if err != nil {
		return "", err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", ErrEmptyReply
	}
	return answer, nil
}

// RequestChoice sends the rendered options and returns the option the human
// selected. A reply that matches no option is an error rather than a
// zero-value guess.
func (f *TextFallback) RequestChoice(ctx context.Context, req PickerRequest) (InteractiveChoice, error) {
	if len(req.Options) == 0 {
		return InteractiveChoice{}, ErrNoPickerOptions
	}
	if err := f.ready(); err != nil {
		return InteractiveChoice{}, err
	}
	if err := f.Send(ctx, FormatPickerText(req)); err != nil {
		return InteractiveChoice{}, err
	}
	reply, err := f.Reply(ctx)
	if err != nil {
		return InteractiveChoice{}, err
	}
	choice, ok := ParsePickerReply(req, reply)
	if !ok {
		return InteractiveChoice{}, fmt.Errorf("interactive: reply %q does not match any offered option", strings.TrimSpace(reply))
	}
	return choice, nil
}

// ready reports whether both transport halves are present before any send is
// attempted, so a half-built fallback fails at the call rather than
// mid-interaction.
func (f *TextFallback) ready() error {
	if f == nil || f.Send == nil || f.Reply == nil {
		return ErrTextFallbackNotConfigured
	}
	return nil
}
