package messaging

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ── Test doubles ────────────────────────────────────────────────────
//
// These are the "test double adapter, not a live platform" the bead calls
// for: they exercise both the native method set and the capability report,
// and one of them deliberately mis-reports so the regression is pinned.

// nativeInteractive is a well-behaved adapter: it implements both interactive
// interfaces and reports both capabilities.
type nativeInteractive struct {
	answer    string
	choice    InteractiveChoice
	lastReq   any
	choiceErr error
}

func (d *nativeInteractive) RequestClarification(_ context.Context, req ClarifyRequest) (string, error) {
	d.lastReq = req
	return d.answer, nil
}

func (d *nativeInteractive) RequestChoice(_ context.Context, req PickerRequest) (InteractiveChoice, error) {
	d.lastReq = req
	return d.choice, d.choiceErr
}

func (d *nativeInteractive) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{Clarify: true, Picker: true}
}

// clarifierOnly implements only the clarify half and reports it honestly.
type clarifierOnly struct{}

func (clarifierOnly) RequestClarification(context.Context, ClarifyRequest) (string, error) {
	return "yes", nil
}

func (clarifierOnly) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{Clarify: true}
}

// interactiveMisreport implements both interfaces but denies both
// capabilities, so the capability -- not the method set -- is what callers
// must trust.
type interactiveMisreport struct{ *nativeInteractive }

func (interactiveMisreport) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{}
}

// interactiveOverclaim claims both capabilities without implementing either
// interface.
type interactiveOverclaim struct{}

func (interactiveOverclaim) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{Clarify: true, Picker: true}
}

// approvalDouble renders approvals and reports the capability.
type approvalDouble struct{}

func (approvalDouble) RequestApproval(context.Context, string, string) (ApprovalDecision, error) {
	return ApprovalApproved, nil
}

func (approvalDouble) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{Approval: true}
}

// approvalMisreport renders approvals but denies the capability.
type approvalMisreport struct{ approvalDouble }

func (approvalMisreport) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{}
}

// ── Capability agreement ────────────────────────────────────────────

// A claimed interactive capability must agree with the implemented interface
// for an honest adapter: the flag is the caller's way to know, ahead of a type
// assertion, whether an interaction is worth attempting, and a mismatch makes
// that signal lie. Over- and under-claiming are covered by the XerOf tests,
// which are the callers that must tolerate a mis-report.
func TestAdapterCapabilitiesInteractiveMatchesImplementedInterfaces(t *testing.T) {
	tests := []struct {
		name string
		send any
		clar bool
		pick bool
		appr bool
	}{
		{
			name: "implements and reports both interactive halves",
			send: &nativeInteractive{},
			clar: true,
			pick: true,
		},
		{
			name: "implements clarify only",
			send: clarifierOnly{},
			clar: true,
			pick: false,
		},
		{
			name: "implements and reports approval",
			send: approvalDouble{},
			appr: true,
		},
		{
			name: "implements nothing",
			send: struct{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := CapabilitiesOf(tt.send)
			if caps.Clarify != tt.clar {
				t.Errorf("CapabilitiesOf().Clarify = %v, want %v", caps.Clarify, tt.clar)
			}
			if caps.Picker != tt.pick {
				t.Errorf("CapabilitiesOf().Picker = %v, want %v", caps.Picker, tt.pick)
			}
			if caps.Approval != tt.appr {
				t.Errorf("CapabilitiesOf().Approval = %v, want %v", caps.Approval, tt.appr)
			}

			// The regression the acceptance criteria calls for: an honest
			// adapter's claim must match its method set in both directions.
			_, isClarifier := tt.send.(ClarifyRequester)
			_, isPicker := tt.send.(PickerRequester)
			_, isApprover := tt.send.(ApprovalRequester)
			if caps.Clarify != isClarifier {
				t.Errorf("Clarify claim (%v) disagrees with ClarifyRequester implementation (%v)", caps.Clarify, isClarifier)
			}
			if caps.Picker != isPicker {
				t.Errorf("Picker claim (%v) disagrees with PickerRequester implementation (%v)", caps.Picker, isPicker)
			}
			if caps.Approval != isApprover {
				t.Errorf("Approval claim (%v) disagrees with ApprovalRequester implementation (%v)", caps.Approval, isApprover)
			}
		})
	}
}

// ── XerOf degradation ───────────────────────────────────────────────

func TestClarifierOf(t *testing.T) {
	tests := []struct {
		name   string
		sender any
		want   bool
	}{
		{name: "capability and interface agree", sender: &nativeInteractive{}, want: true},
		{name: "interface without capability", sender: interactiveMisreport{&nativeInteractive{}}, want: false},
		{name: "capability without interface", sender: interactiveOverclaim{}, want: false},
		{name: "neither capability nor interface", sender: struct{}{}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ClarifierOf(tt.sender)
			if ok != tt.want {
				t.Fatalf("ClarifierOf(%s) ok = %v, want %v", tt.name, ok, tt.want)
			}
			if ok && got == nil {
				t.Fatalf("ClarifierOf(%s) reported ok with a nil requester", tt.name)
			}
			if !ok && got != nil {
				t.Fatalf("ClarifierOf(%s) reported !ok with a non-nil requester", tt.name)
			}
		})
	}
}

func TestPickerOf(t *testing.T) {
	tests := []struct {
		name   string
		sender any
		want   bool
	}{
		{name: "capability and interface agree", sender: &nativeInteractive{}, want: true},
		{name: "interface without capability", sender: interactiveMisreport{&nativeInteractive{}}, want: false},
		{name: "capability without interface", sender: interactiveOverclaim{}, want: false},
		{name: "clarify-only adapter has no picker", sender: clarifierOnly{}, want: false},
		{name: "neither capability nor interface", sender: struct{}{}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := PickerOf(tt.sender)
			if ok != tt.want {
				t.Fatalf("PickerOf(%s) ok = %v, want %v", tt.name, ok, tt.want)
			}
			if ok && got == nil {
				t.Fatalf("PickerOf(%s) reported ok with a nil requester", tt.name)
			}
			if !ok && got != nil {
				t.Fatalf("PickerOf(%s) reported !ok with a non-nil requester", tt.name)
			}
		})
	}
}

func TestApproverOf(t *testing.T) {
	tests := []struct {
		name   string
		sender any
		want   bool
	}{
		{name: "capability and interface agree", sender: approvalDouble{}, want: true},
		{name: "interface without capability", sender: approvalMisreport{}, want: false},
		{name: "neither capability nor interface", sender: struct{}{}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ApproverOf(tt.sender)
			if ok != tt.want {
				t.Fatalf("ApproverOf(%s) ok = %v, want %v", tt.name, ok, tt.want)
			}
			if ok && got == nil {
				t.Fatalf("ApproverOf(%s) reported ok with a nil requester", tt.name)
			}
			if !ok && got != nil {
				t.Fatalf("ApproverOf(%s) reported !ok with a non-nil requester", tt.name)
			}
		})
	}
}

// ── Text fallback rendering and parsing ─────────────────────────────

func TestFormatClarifyText(t *testing.T) {
	tests := []struct {
		name string
		req  ClarifyRequest
		want string
	}{
		{
			name: "question with suggestions",
			req: ClarifyRequest{
				Question:    "Which environment?",
				Suggestions: []InteractiveChoice{{ID: "staging", Label: "Staging"}, {ID: "prod", Label: "Production"}},
			},
			want: "Which environment?\n\nSuggestions:\n1) Staging\n2) Production\n\nReply with your answer.",
		},
		{
			name: "question without suggestions",
			req:  ClarifyRequest{Question: "What did you mean?"},
			want: "What did you mean?\n\nReply with your answer.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatClarifyText(tt.req); got != tt.want {
				t.Errorf("FormatClarifyText() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestFormatPickerText(t *testing.T) {
	tests := []struct {
		name string
		req  PickerRequest
		want string
	}{
		{
			name: "prompt with options",
			req: PickerRequest{
				Prompt:  "Pick a branch",
				Options: []InteractiveChoice{{ID: "a", Label: "main"}, {ID: "b", Label: "release"}},
			},
			want: "Pick a branch\n\n1) main\n2) release\n\nReply with the number of your choice.",
		},
		{
			name: "prompt with no options",
			req:  PickerRequest{Prompt: "Pick a branch"},
			want: "Pick a branch\n\nNo options were offered.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatPickerText(tt.req); got != tt.want {
				t.Errorf("FormatPickerText() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestParsePickerReply(t *testing.T) {
	req := PickerRequest{
		Options: []InteractiveChoice{
			{ID: "main", Label: "main branch"},
			{ID: "release", Label: "release branch"},
		},
	}
	tests := []struct {
		name   string
		reply  string
		wantID string
		wantOK bool
	}{
		{name: "one-based index", reply: "2", wantID: "release", wantOK: true},
		{name: "index with whitespace", reply: "  1  ", wantID: "main", wantOK: true},
		{name: "exact id", reply: "release", wantID: "release", wantOK: true},
		{name: "id case-insensitive", reply: "MAIN", wantID: "main", wantOK: true},
		{name: "exact label", reply: "release branch", wantID: "release", wantOK: true},
		{name: "label case-insensitive", reply: "Main Branch", wantID: "main", wantOK: true},
		{name: "index out of range", reply: "3", wantID: "", wantOK: false},
		{name: "zero index", reply: "0", wantID: "", wantOK: false},
		{name: "unmatched text", reply: "something else", wantID: "", wantOK: false},
		{name: "empty reply", reply: "   ", wantID: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParsePickerReply(req, tt.reply)
			if ok != tt.wantOK {
				t.Fatalf("ParsePickerReply(%q) ok = %v, want %v", tt.reply, ok, tt.wantOK)
			}
			if got.ID != tt.wantID {
				t.Errorf("ParsePickerReply(%q).ID = %q, want %q", tt.reply, got.ID, tt.wantID)
			}
		})
	}
}

// ── Text fallback degrade path ──────────────────────────────────────

// recordingFallback wires a TextFallback to a captured prompt and a scripted
// reply, the shape a text-only channel gives it.
func recordingFallback(reply string, replyErr error) (*TextFallback, *string) {
	var sent string
	f := &TextFallback{
		Send: func(_ context.Context, text string) error {
			sent = text
			return nil
		},
		Reply: func(context.Context) (string, error) {
			return reply, replyErr
		},
	}
	return f, &sent
}

func TestTextFallbackRequestClarification(t *testing.T) {
	f, sent := recordingFallback("  staged the release  ", nil)
	got, err := f.RequestClarification(context.Background(), ClarifyRequest{Question: "What next?"})
	if err != nil {
		t.Fatalf("RequestClarification: %v", err)
	}
	if got != "staged the release" {
		t.Errorf("answer = %q, want %q", got, "staged the release")
	}
	if !strings.Contains(*sent, "What next?") {
		t.Errorf("sent prompt %q does not carry the question", *sent)
	}
}

func TestTextFallbackRequestClarificationPropagatesReplyError(t *testing.T) {
	wantErr := errors.New("chat closed")
	f, _ := recordingFallback("", wantErr)
	if _, err := f.RequestClarification(context.Background(), ClarifyRequest{Question: "?"}); !errors.Is(err, wantErr) {
		t.Fatalf("RequestClarification error = %v, want %v", err, wantErr)
	}
}

func TestTextFallbackRequestChoice(t *testing.T) {
	f, sent := recordingFallback("2", nil)
	req := PickerRequest{
		Prompt:  "Pick",
		Options: []InteractiveChoice{{ID: "a", Label: "first"}, {ID: "b", Label: "second"}},
	}
	got, err := f.RequestChoice(context.Background(), req)
	if err != nil {
		t.Fatalf("RequestChoice: %v", err)
	}
	if got.ID != "b" {
		t.Errorf("chosen ID = %q, want %q", got.ID, "b")
	}
	if !strings.Contains(*sent, "1) first") || !strings.Contains(*sent, "2) second") {
		t.Errorf("sent prompt %q does not carry the numbered options", *sent)
	}
}

// The degrade path the bead calls for: an adapter with no native UI still
// carries the selection rather than failing.
func TestTextFallbackDegradesToTextWithoutNativeUI(t *testing.T) {
	f, _ := recordingFallback("release", nil)
	got, err := f.RequestChoice(context.Background(), PickerRequest{
		Prompt:  "Pick a branch",
		Options: []InteractiveChoice{{ID: "main", Label: "main"}, {ID: "release", Label: "release"}},
	})
	if err != nil {
		t.Fatalf("RequestChoice: %v", err)
	}
	if got.ID != "release" {
		t.Errorf("chosen ID = %q, want %q", got.ID, "release")
	}
}

func TestTextFallbackRequestChoiceRejectsUnknownReply(t *testing.T) {
	f, _ := recordingFallback("nonsense", nil)
	_, err := f.RequestChoice(context.Background(), PickerRequest{
		Prompt:  "Pick",
		Options: []InteractiveChoice{{ID: "a", Label: "first"}},
	})
	if err == nil {
		t.Fatal("RequestChoice returned a choice for an unmatched reply")
	}
	if errors.Is(err, ErrNoPickerOptions) {
		t.Fatalf("RequestChoice returned ErrNoPickerOptions for a matched-but-unknown reply: %v", err)
	}
}

func TestTextFallbackRequestChoiceRejectsEmptyOptions(t *testing.T) {
	f, _ := recordingFallback("1", nil)
	_, err := f.RequestChoice(context.Background(), PickerRequest{Prompt: "Pick"})
	if !errors.Is(err, ErrNoPickerOptions) {
		t.Fatalf("RequestChoice error = %v, want ErrNoPickerOptions", err)
	}
}

func TestTextFallbackRequestClarificationRejectsEmptyReply(t *testing.T) {
	f, _ := recordingFallback("   ", nil)
	_, err := f.RequestClarification(context.Background(), ClarifyRequest{Question: "?"})
	if !errors.Is(err, ErrEmptyReply) {
		t.Fatalf("RequestClarification error = %v, want ErrEmptyReply", err)
	}
}

func TestTextFallbackUnconfigured(t *testing.T) {
	var f TextFallback
	if _, err := f.RequestClarification(context.Background(), ClarifyRequest{Question: "?"}); !errors.Is(err, ErrTextFallbackNotConfigured) {
		t.Fatalf("RequestClarification error = %v, want ErrTextFallbackNotConfigured", err)
	}
	if _, err := f.RequestChoice(context.Background(), PickerRequest{Prompt: "Pick", Options: []InteractiveChoice{{ID: "a"}}}); !errors.Is(err, ErrTextFallbackNotConfigured) {
		t.Fatalf("RequestChoice error = %v, want ErrTextFallbackNotConfigured", err)
	}
}

func TestInteractiveOfTrustsCapabilityReport(t *testing.T) {
	got := InteractiveOf(&nativeInteractive{})
	if !got.Carries() {
		t.Fatal("a channel reporting Clarify and Picker produced no requesters")
	}
	if got.Clarifier == nil || got.Picker == nil {
		t.Fatalf("Interactive = %+v, want both requesters", got)
	}

	// The capability is the contract: a sender that claims an interaction it
	// does not implement must contribute nothing rather than be called.
	if over := InteractiveOf(interactiveOverclaim{}); over.Carries() {
		t.Errorf("over-claiming adapter produced requesters: %+v", over)
	}
	if (Interactive{}).Carries() {
		t.Error("the zero Interactive reports that it carries an interaction")
	}
}

func TestInteractiveCarrierRoundTrip(t *testing.T) {
	ctx := WithInteractive(context.Background(), InteractiveOf(&nativeInteractive{}))
	got := InteractiveFromContext(ctx)
	if !got.Carries() {
		t.Fatalf("InteractiveFromContext = %+v, want the stored requesters", got)
	}
	if InteractiveFromContext(context.Background()).Carries() {
		t.Error("a context without a carrier reported one")
	}
}
