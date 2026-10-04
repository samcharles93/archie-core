package config

// ChatFrontEnd is one conversational front-end [chat] can enable. ID is its
// stable identifier.
type ChatFrontEnd struct {
	ID         string
	Name       string
	Configured bool
}

// FrontEnds returns every chat front-end this configuration enables, in
// dashboard order. Whitespace-only values do not count as configured.
func (c ChatConfig) FrontEnds() []ChatFrontEnd {
	return []ChatFrontEnd{
		{
			ID:         "telegram",
			Name:       "Telegram",
			Configured: c.Telegram.Token != (SecretRef{}),
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
