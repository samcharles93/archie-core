package releaseupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"

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
	release *release
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
			Installed: installed, Available: release.version,
			Reference: release.asset, Changelog: changelogURL,
		}
	}
	return Snapshot{Components: []Component{
		component("daemon", "Gateway", installedVersion(buildinfo.Version)),
		component("agent", "Runtime", installedAgent()),
	}}, nil
}

func (c *GitHubCatalog) detect(ctx context.Context, channel, pin string) (*release, error) {
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

// release is one published version and the zip that installs it here.
type release struct {
	version string
	asset   string
}

const releasesURL = "https://api.github.com/repos/" + ReleaseSlug + "/releases?per_page=100"

type githubRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
	} `json:"assets"`
}

// detectRelease resolves channel to one published release. A release with no
// zip for this platform, or no SHA256SUMS to verify it against, is not
// installable, so it is never offered.
func detectRelease(ctx context.Context, channel, pin string) (*release, error) {
	if err := ValidateChannel(channel, pin); err != nil {
		return nil, err
	}
	releases, err := listReleases(ctx)
	if err != nil {
		return nil, fmt.Errorf("find releases: %w", err)
	}
	if found := pick(releases, channel, pin); found != nil {
		return found, nil
	}
	return nil, errors.New("no published release with a linux-amd64 zip matches the channel")
}

func listReleases(ctx context.Context) ([]githubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var releases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}
	return releases, nil
}

// pick returns the newest installable release on channel.
func pick(releases []githubRelease, channel, pin string) *release {
	var best *release
	for _, r := range releases {
		if !onChannel(r, channel, pin) {
			continue
		}
		asset := installable(r)
		if asset == "" {
			continue
		}
		if best == nil || semver.Compare(r.TagName, "v"+best.version) > 0 {
			best = &release{version: strings.TrimPrefix(r.TagName, "v"), asset: asset}
		}
	}
	return best
}

// onChannel: stable skips prereleases, next includes them, and exact-pin takes
// only the pinned tag.
func onChannel(r githubRelease, channel, pin string) bool {
	if r.Draft || !semver.IsValid(r.TagName) {
		return false
	}
	switch channel {
	case "exact-pin":
		return r.TagName == "v"+strings.TrimPrefix(pin, "v")
	case "next":
		return true
	default:
		return !r.Prerelease
	}
}

// installable names the release's linux-amd64 zip, or "" when it has none or
// no SHA256SUMS to verify it against.
func installable(r githubRelease) string {
	asset, sums := "", false
	for _, a := range r.Assets {
		switch {
		case a.Name == "SHA256SUMS":
			sums = true
		case strings.HasSuffix(a.Name, "-linux-amd64.zip"):
			asset = a.Name
		}
	}
	if !sums {
		return ""
	}
	return asset
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
