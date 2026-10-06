package releaseupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creativeprojects/go-selfupdate"

	"github.com/samcharles93/archie-core/internal/buildinfo"
)

// ReleaseSlug is the repository releases are published to.
const ReleaseSlug = "samcharles93/archie-core"

const changelogURL = "https://github.com/" + ReleaseSlug + "/blob/main/CHANGELOG.md"

// buildSuffix is the git-describe tail of an untagged build (1.7.0-4-g0736708).
var buildSuffix = regexp.MustCompile(`-[0-9]+-g[0-9a-f]+$`)

// GitHubCatalog finds the newest release on the selected channel that ships a
// linux-amd64 zip, and reports it against this build. Only one version covers
// every component, so the daemon and agent are offered the same release.
type GitHubCatalog struct {
	// Channel returns the release channel and pin in force.
	Channel func(context.Context) (channel, pin string, err error)

	// A found release is reused for checkTTL: unauthenticated GitHub API
	// calls are limited to 60 an hour per address.
	mu      sync.Mutex
	key     string
	at      time.Time
	release *selfupdate.Release
}

const checkTTL = 10 * time.Minute

func (c *GitHubCatalog) Check(ctx context.Context) (Snapshot, error) {
	channel, pin, err := c.Channel(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if channel == "" {
		channel = "stable"
	}
	release, err := c.detect(ctx, channel, pin)
	if err != nil {
		return Snapshot{}, err
	}
	component := func(id, label, installed string) Component {
		return Component{
			ID: id, Label: label,
			Installed: installed, Available: release.Version(),
			Reference: release.AssetName, Changelog: changelogURL,
		}
	}
	return Snapshot{Components: []Component{
		component("daemon", "Gateway", installedVersion(buildinfo.Version)),
		component("agent", "Runtime", installedAgent()),
	}}, nil
}

func (c *GitHubCatalog) detect(ctx context.Context, channel, pin string) (*selfupdate.Release, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := channel + "\x00" + pin
	if c.release != nil && c.key == key && time.Since(c.at) < checkTTL {
		return c.release, nil
	}
	release, err := detectRelease(ctx, channel, pin)
	if err != nil {
		return nil, err
	}
	c.key, c.at, c.release = key, time.Now(), release
	return release, nil
}

// detectRelease resolves channel to one published release. A release with no
// zip for this platform is not installable, so it is never offered.
func detectRelease(ctx context.Context, channel, pin string) (*selfupdate.Release, error) {
	if err := ValidateChannel(channel, pin); err != nil {
		return nil, err
	}
	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Validator:  &selfupdate.ChecksumValidator{UniqueFilename: "SHA256SUMS"},
		Filters:    []string{`-linux-amd64\.zip$`},
		OS:         "linux",
		Arch:       "amd64",
		Prerelease: channel == "next",
	})
	if err != nil {
		return nil, err
	}
	repository := selfupdate.ParseSlug(ReleaseSlug)
	var (
		release *selfupdate.Release
		found   bool
	)
	if channel == "exact-pin" {
		release, found, err = updater.DetectVersion(ctx, repository, "v"+strings.TrimPrefix(pin, "v"))
	} else {
		release, found, err = updater.DetectLatest(ctx, repository)
	}
	if err != nil {
		return nil, fmt.Errorf("find releases: %w", err)
	}
	if !found {
		return nil, errors.New("no published release with a linux-amd64 zip matches the channel")
	}
	return release, nil
}

func installedVersion(version string) string {
	return buildSuffix.ReplaceAllString(version, "")
}

// installedAgent is the image the installer last switched to, recorded in a
// sidecar beside the binaries; without one it is the runtime this release was
// cut with. A malformed sidecar is unknown, not guessed.
func installedAgent() string {
	exe, err := os.Executable()
	if err != nil {
		return installedVersion(buildinfo.Runtime)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "archie-agent.version"))
	if errors.Is(err, os.ErrNotExist) {
		return installedVersion(buildinfo.Runtime)
	}
	version := strings.TrimSpace(string(data))
	if err != nil || strings.ContainsAny(version, " \n") || ValidateChannel("exact-pin", version) != nil {
		return ""
	}
	return installedVersion(version)
}
