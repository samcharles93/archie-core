package configuration

// DeniedKeys are the config keys the runtime overlay may never set, mapped
// to the reason shown to the operator.
var DeniedKeys = map[string]string{
	"database_url": "required for bootstrap; cannot be changed at runtime",
	"state_dir":    "required for bootstrap; cannot be changed at runtime",
	"work_dir":     "pins the daemon's working layout; cannot be changed at runtime",
}
