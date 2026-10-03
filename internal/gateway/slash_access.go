package gateway

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// SlashRole is a chat sender's standing for slash-command access.
type SlashRole string

const (
	// SlashRoleAdmin may run every slash command.
	SlashRoleAdmin SlashRole = "admin"
	// SlashRoleUser is a known non-admin sender: it may run the ordinary
	// commands and any admin command delegated to it.
	SlashRoleUser SlashRole = "user"
	// SlashRoleUnknown is a sender no list names. It may run nothing.
	SlashRoleUnknown SlashRole = "unknown"
)

// SlashDecision is the outcome of one slash-access check.
type SlashDecision struct {
	Allowed bool
	Role    SlashRole
	// Reason is empty when Allowed, else the refusal cause: "unknown
	// sender" or "not permitted".
	Reason string
}

// SlashDenial is one refused slash command.
type SlashDenial struct {
	Sender  string
	Command string
	Role    SlashRole
	Reason  string
}

// SlashDenialRecorder receives one record per refused slash command, so a
// deployment can persist the refusal where an operator reviews it.
type SlashDenialRecorder interface {
	RecordSlashDenial(ctx context.Context, denial SlashDenial) error
}

// slashAdminOnly names the commands that reconfigure the installation or the
// model it runs, and therefore require an admin. Every other command in
// messaging.LocalCommands is an ordinary user command; task control
// (/approve, /cancel) is deliberately ordinary here because it is scoped by
// task ownership at its own layer.
var slashAdminOnly = map[string]bool{
	"/model":       true,
	"/personality": true,
	"/settings":    true,
	"/update":      true,
	"/restart":     true,
}

// SlashAccessPolicy decides which slash commands a chat sender may run. It
// is deny-by-default: a sender named by neither list may run nothing, so the
// zero value refuses every command.
//
// AllowAdminFrom names the senders with full access. UserAllowedCommands
// names the non-admin senders and, for each, the admin commands delegated to
// it (the ordinary commands are always available to a named user).
//
// The Router consults the policy before dispatching any local command, so a
// refusal executes none of the handler and is visible to the sender and to the
// daemon log; a wired SlashDenials sink records it for audit. The policy is
// keyed on the channel-native sender, not on an org principal: a chat message
// carries no identity the access chain can evaluate, so this is the layer that
// can decide at the point the command is typed. A composition with no policy
// leaves every command available, which is the pre-policy behaviour.
type SlashAccessPolicy struct {
	AllowAdminFrom      []string
	UserAllowedCommands map[string][]string
}

// Role reports the sender's standing: admin when AllowAdminFrom names it,
// user when UserAllowedCommands names it, unknown otherwise. An admin entry
// wins over a user entry for the same sender.
func (p *SlashAccessPolicy) Role(sender string) SlashRole {
	if p == nil {
		return SlashRoleUnknown
	}
	if slices.Contains(p.AllowAdminFrom, sender) {
		return SlashRoleAdmin
	}
	if _, ok := p.UserAllowedCommands[sender]; ok {
		return SlashRoleUser
	}
	return SlashRoleUnknown
}

// Decide reports whether sender may run command. An admin may run anything; a
// user may run the ordinary commands and any admin command delegated to it;
// an unknown sender may run nothing.
func (p *SlashAccessPolicy) Decide(sender, command string) SlashDecision {
	cmd := slashCommandName(command)
	role := p.Role(sender)
	switch role {
	case SlashRoleAdmin:
		return SlashDecision{Allowed: true, Role: role}
	case SlashRoleUser:
		if !slashAdminOnly[cmd] || slices.Contains(p.UserAllowedCommands[sender], cmd) {
			return SlashDecision{Allowed: true, Role: role}
		}
		return SlashDecision{Role: role, Reason: "not permitted"}
	default:
		return SlashDecision{Role: SlashRoleUnknown, Reason: "unknown sender"}
	}
}

// slashCommandName reduces a command token to its bare name: leading and
// trailing space, an @gateway mention, and any argument are dropped, so
// "/model@archie gpt-5" decides as "/model".
func slashCommandName(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	token := fields[0]
	if i := strings.IndexByte(token, '@'); i >= 0 {
		token = token[:i]
	}
	return token
}

// slashSender identifies the party a decision is made for. SenderID is the
// channel-native stable id; Sender is the display form, used only when a
// channel offers no stable id.
func slashSender(in Inbound) string {
	if in.Message.SenderID != "" {
		return in.Message.SenderID
	}
	return in.Message.Sender
}

// checkSlashAccess enforces the policy on a local command, returning
// (reply, true) when it is refused. A nil policy allows. Non-command text
// never reaches the gate.
func (r *Router) checkSlashAccess(ctx context.Context, in Inbound, cmd string) (string, bool) {
	if r.SlashAccess == nil || !strings.HasPrefix(cmd, "/") {
		return "", false
	}
	sender := slashSender(in)
	decision := r.SlashAccess.Decide(sender, cmd)
	if decision.Allowed {
		return "", false
	}
	denial := SlashDenial{Sender: sender, Command: cmd, Role: decision.Role, Reason: decision.Reason}
	if r.SlashDenials != nil {
		if err := r.SlashDenials.RecordSlashDenial(ctx, denial); err != nil && r.Log != nil {
			r.Log.Error("record slash denial", "err", err)
		}
	}
	if r.Log != nil {
		r.Log.Warn("slash command refused",
			"sender", sender, "command", cmd, "role", string(decision.Role), "reason", decision.Reason)
	}
	return fmt.Sprintf("You are not permitted to run %s.", cmd), true
}
