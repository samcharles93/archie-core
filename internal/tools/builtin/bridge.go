package builtin

import (
	"context"
	"errors"
)

var (
	ErrInteractiveUnsupported = errors.New("interactive prompts are not supported in this mode")
	ErrInteractiveCanceled    = errors.New("interactive prompt was canceled")
)

// UIBridge allows tools to interact with the user through the TUI.
// This interface is satisfied by the extension/ui bridge implementation.
type UIBridge interface {
	Confirm(ctx context.Context, title, description string) (bool, error)
	Select(ctx context.Context, title string, options []string) (string, error)
	Input(ctx context.Context, title, placeholder string) (string, error)
	Notify(title, level string)
	Log(chunk string)
	// SessionID returns the current tool call's session, or "".
	SessionID() string
}

type NonInteractiveBridge struct{}

func (NonInteractiveBridge) SessionID() string { return "" }

func (NonInteractiveBridge) Confirm(context.Context, string, string) (bool, error) {
	return false, ErrInteractiveUnsupported
}

func (NonInteractiveBridge) Select(context.Context, string, []string) (string, error) {
	return "", ErrInteractiveUnsupported
}

func (NonInteractiveBridge) Input(context.Context, string, string) (string, error) {
	return "", ErrInteractiveUnsupported
}

func (NonInteractiveBridge) Notify(string, string) {}

func (NonInteractiveBridge) Log(string) {}
