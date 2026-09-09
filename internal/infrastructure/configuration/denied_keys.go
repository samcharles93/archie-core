package configuration

// DeniedKeys are the config keys the runtime overlay may never set, mapped
// to the reason shown to the operator. db_path is the daemon's own bootstrap
// path -- the daemon must read it before it can open the overlay store --
// and work_dir pins the whole working layout. Enforced at write time (the
// API returns 4xx) and again in the overlay's Set, not silently dropped at
// read time. Owned here rather than in the overlay package so the webui
// config renderer can reference the policy without linking the overlay's
// SQLite store (archie-core-8cda.5.6).
var DeniedKeys = map[string]string{
	"db_path":  "required for bootstrap; cannot be changed at runtime",
	"work_dir": "pins the daemon's working layout; cannot be changed at runtime",
}
