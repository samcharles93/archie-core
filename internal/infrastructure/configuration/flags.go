package configuration

import "os"

// envSkipOverlay names the environment variable that disables runtime
// config overlays. It is the recovery hatch for an overlay write that
// leaves the daemon unable to boot: set ARCHIE_SKIP_CONFIG_OVERLAY=1
// and the daemon boots from file config alone. The same switch exists
// as the --no-config-overlay flag on archied.
const envSkipOverlay = "ARCHIE_SKIP_CONFIG_OVERLAY"

// SkipOverlay reports whether runtime config overlays are disabled via
// ARCHIE_SKIP_CONFIG_OVERLAY=1. The overlay source (a later step of the
// config-editing work) consults this before loading; cmd/archied
// OR-s its --no-config-overlay flag with this at startup.
func SkipOverlay() bool {
	return os.Getenv(envSkipOverlay) == "1"
}
