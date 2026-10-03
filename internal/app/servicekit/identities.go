package servicekit

import "github.com/samcharles93/archie-core/internal/config"

// IdentityNames is the agent identities the config names, or the bot user
// alone when it names none.
func IdentityNames(cfg config.Config) []string {
	if len(cfg.Identities) == 0 {
		name := cfg.BotUser
		if name == "" {
			name = "archie"
		}
		return []string{name}
	}
	names := make([]string, 0, len(cfg.Identities))
	for _, value := range cfg.Identities {
		names = append(names, value.Name)
	}
	return names
}
