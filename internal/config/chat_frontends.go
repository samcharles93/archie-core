package config

import "strings"

// ChatFrontEnd is one conversational front-end [chat] can enable.
//
// It carries the answer to "is this front-end configured", and nothing else:
// which process starts it, whether it can be reloaded, and what an operator
// reads about it are channel-lifecycle facts that belong to the daemon, not to
// configuration. ID is the stable identifier every surface keys on (the
// dashboard's channel list, the channel reload route).
type ChatFrontEnd struct {
	ID         string
	Name       string
	Configured bool
}

// FrontEnds reports every conversational front-end this configuration enables,
// in the order the dashboard lists them.
//
// This is the single definition of "a chat channel is configured". It exists
// because that question used to be answered twice from the same config and the
// two answers drifted: the daemon's channel status manager counted the inbound
// mail gateway, and the dashboard projection published for the extracted UI
// process did not, so an email-only deployment read as configured on
// /api/channels and unconfigured on the setup checklist at the same time
// (GitHub #821). Front-ends are enumerated once here so a front-end added to
// [chat] cannot be counted by one surface and missed by the other.
//
// A whitespace-only value is not configured on any front-end: an env var name
// of " " resolves to nothing, and an address of " " cannot be listened on.
func (c ChatConfig) FrontEnds() []ChatFrontEnd {
	return []ChatFrontEnd{
		{
			ID:         "telegram",
			Name:       "Telegram",
			Configured: c.Telegram.Token != (SecretRef{}),
		},
		{
			ID:         "email",
			Name:       "Email",
			Configured: strings.TrimSpace(c.Email.ListenAddr) != "",
		},
		{
			ID:         "webhook",
			Name:       "Webhook gateway",
			Configured: strings.TrimSpace(c.WebhookAddr) != "",
		},
	}
}

// AnyFrontEndConfigured reports whether at least one conversational front-end
// has credentials. The setup checklist asks this as a yes/no question; every
// other consumer wants the per-front-end detail FrontEnds returns.
func (c ChatConfig) AnyFrontEndConfigured() bool {
	for _, frontEnd := range c.FrontEnds() {
		if frontEnd.Configured {
			return true
		}
	}
	return false
}
