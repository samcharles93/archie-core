package releaseupdate

import (
	"fmt"
	"regexp"
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

// SelectRelease compares release versions, never git-describe strings. Exact
// pins still have to exist in the catalog; a pin cannot invent a release.
func SelectRelease(tags []string, channel, pin string) (string, error) {
	if channel == "" {
		channel = "stable"
	}
	if err := ValidateChannel(channel, pin); err != nil {
		return "", err
	}
	pin = strings.TrimPrefix(pin, "v")
	fullVersion := regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+([+-].*)?$`)
	newest := ""
	for _, tag := range tags {
		tag = strings.TrimPrefix(tag, "refs/tags/")
		tag = strings.TrimPrefix(tag, "archied/")
		if !fullVersion.MatchString(tag) || !semver.IsValid(tag) {
			continue
		}
		if !releaseMatchesChannel(tag, channel, pin) {
			continue
		}
		if semver.Compare(tag, newest) > 0 {
			newest = tag
		}
	}
	if newest == "" {
		return "", fmt.Errorf("no release found for channel %s (pin %s)", channel, pin)
	}
	return strings.TrimPrefix(newest, "v"), nil
}

func releaseMatchesChannel(tag, channel, pin string) bool {
	switch channel {
	case "stable":
		return semver.Prerelease(tag) == ""
	case "exact-pin":
		return tag == "v"+pin
	default:
		return true
	}
}
