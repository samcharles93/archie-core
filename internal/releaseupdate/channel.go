package releaseupdate

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// ValidateChannel refuses an unknown channel, and an exact pin that is not a
// semantic version.
func ValidateChannel(channel, pin string) error {
	if channel != "stable" && channel != "next" && channel != "exact-pin" {
		return fmt.Errorf("unknown release channel %q", channel)
	}
	if channel == "exact-pin" && !semver.IsValid("v"+strings.TrimPrefix(pin, "v")) {
		return fmt.Errorf("exact-pin requires a semantic version")
	}
	return nil
}
