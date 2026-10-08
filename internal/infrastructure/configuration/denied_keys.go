package configuration

// DeniedKeys are the config keys the runtime overlay may never set, mapped
// to the reason shown to the operator.
var DeniedKeys = map[string]string{
	"database_url":                      "required for bootstrap; cannot be changed at runtime",
	"state_dir":                         "required for bootstrap; cannot be changed at runtime",
	"work_dir":                          "pins the daemon's working layout; cannot be changed at runtime",
	"forge.type":                        "Constructs the forge client at startup.",
	"forge.host":                        "Constructs the forge client at startup.",
	"forge.token":                       "Binds the forge account at startup.",
	"forge.intake":                      "Constructs the poll and webhook intake at startup.",
	"forge.webhook_secret":              "Binds the webhook receiver at startup.",
	"forge.webhook_addr":                "Binds the webhook listener at startup.",
	"bot_user":                          "Pins the daemon's forge identity and task-grant namespace at startup.",
	"bot_email":                         "Pins the forge account's commit identity at startup.",
	"org":                               "Pins the deployment's org at startup.",
	"memory.engine":                     "Constructs the memory engine at startup.",
	"capture.*":                         "Constructs the dashboard capture receiver at startup.",
	"log.level":                         "Constructs each service's logging handlers at startup.",
	"log.*":                             "Constructs each service's logging handlers at startup.",
	"tools.mcp_servers[].headers":       "Process credentials supplied at startup; values are never published.",
	"containers.registry_auth":          "Resolves the registry credential when the container pool starts.",
	"services.*":                        "Binds service listeners and remote clients at startup.",
	"nats.*":                            "Constructs the broker connection at startup.",
	"web.*":                             "Binds the dashboard listener and authentication at startup.",
	"health.*":                          "Constructs health listeners and probes at startup.",
	"bindings.encryption_key":           "Selects the State Store encryption key at startup.",
	"bindings.previous_encryption_keys": "Selects the State Store's previous encryption key at startup.",
	"identities[]":                      "Constructs each forge account and task-grant namespace at startup.",
}
