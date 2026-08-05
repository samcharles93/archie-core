// Package releaseannounce turns versioned CHANGELOG sections into one-time
// upgrade notifications.
package releaseannounce

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Sender = func(ctx context.Context, recipient int64, message string) error

type Announcer struct {
	StatePath  string
	Components []Component
}

type Component struct {
	ID            string
	Label         string
	Version       string
	ChangelogPath string
}

func (a Announcer) Announce(ctx context.Context, recipients []int64, send Sender) error {
	if len(recipients) == 0 {
		return nil
	}
	if !hasReleasedComponent(a.Components) {
		return nil
	}
	if send == nil {
		return errors.New("release announcement sender is nil")
	}
	state, err := loadState(a.StatePath)
	if err != nil {
		return err
	}

	var errs []error
	for _, recipient := range recipients {
		if err := a.announceTo(ctx, recipient, send, state); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func hasReleasedComponent(components []Component) bool {
	for _, component := range components {
		if _, valid := parseReleaseVersion(component.Version); valid {
			return true
		}
	}
	return false
}

// announceTo sends one recipient the release announcement if it is their
// first time, or if their components have changed since the last check.
func (a Announcer) announceTo(ctx context.Context, recipient int64, send Sender, state map[string]string) error {
	prefix := strconv.FormatInt(recipient, 10) + ":"

	if !recipientSeen(state, prefix, a.Components) {
		return a.baselineRecipient(state, prefix)
	}

	changed, stateChanged := a.detectChanged(state, prefix)
	if len(changed) == 0 {
		if stateChanged {
			return saveState(a.StatePath, state)
		}
		return nil
	}

	return a.sendAndRecord(ctx, recipient, send, state, prefix, changed)
}

func recipientSeen(state map[string]string, prefix string, components []Component) bool {
	for _, component := range components {
		if _, ok := state[prefix+component.ID]; ok {
			return true
		}
	}
	return false
}

func (a Announcer) baselineRecipient(state map[string]string, prefix string) error {
	for _, component := range a.Components {
		if _, valid := parseReleaseVersion(component.Version); valid {
			state[prefix+component.ID] = component.Version
		}
	}
	return saveState(a.StatePath, state)
}

// detectChanged finds components whose version has increased since the last
// recorded state. It also detects previously-unseen components.
func (a Announcer) detectChanged(state map[string]string, prefix string) (changed map[string]bool, stateChanged bool) {
	changed = make(map[string]bool, len(a.Components))
	for _, component := range a.Components {
		current, validCurrent := parseReleaseVersion(component.Version)
		if !validCurrent {
			continue
		}
		key := prefix + component.ID
		previousText, seen := state[key]
		if !seen {
			state[key] = component.Version
			stateChanged = true
			continue
		}
		previous, validPrevious := parseReleaseVersion(previousText)
		if !validPrevious || compareVersions(current, previous) > 0 {
			changed[component.ID] = true
		}
	}
	return changed, stateChanged
}

// sendAndRecord sends the announcement and updates state on success.
func (a Announcer) sendAndRecord(ctx context.Context, recipient int64, send Sender, state map[string]string, prefix string, changed map[string]bool) error {
	message, err := a.componentMessage(changed)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := send(ctx, recipient, message); err != nil {
		return fmt.Errorf("notify recipient %d: %w", recipient, err)
	}
	for _, component := range a.Components {
		if changed[component.ID] {
			state[prefix+component.ID] = component.Version
		}
	}
	if err := saveState(a.StatePath, state); err != nil {
		return fmt.Errorf("record notification for recipient %d: %w", recipient, err)
	}
	return nil
}

func (a Announcer) componentMessage(changed map[string]bool) (string, error) {
	var message strings.Builder
	message.WriteString("Archie has just been updated.")
	for _, component := range a.Components {
		fmt.Fprintf(&message, "\n\n--- %s ---\n\n", component.Label)
		if !changed[component.ID] {
			fmt.Fprintf(&message, "No %s changes as part of this release.", component.ID)
			continue
		}
		notes, err := changelogSection(component.ChangelogPath, component.Version)
		if err != nil {
			return "", fmt.Errorf("%s changelog: %w", component.ID, err)
		}
		fmt.Fprintf(&message, "%s installed - changes:\n\n%s", component.Version, notes)
	}
	return message.String(), nil
}

type releaseVersion [3]uint64

func parseReleaseVersion(value string) (releaseVersion, bool) {
	if len(value) < len("v0.0.0") || value[0] != 'v' {
		return releaseVersion{}, false
	}
	parts := strings.Split(value[1:], ".")
	if len(parts) != 3 {
		return releaseVersion{}, false
	}
	var parsed releaseVersion
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return releaseVersion{}, false
		}
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return releaseVersion{}, false
		}
		parsed[index] = number
	}
	return parsed, true
}

func compareVersions(left, right releaseVersion) int {
	for index := range left {
		switch {
		case left[index] < right[index]:
			return -1
		case left[index] > right[index]:
			return 1
		}
	}
	return 0
}

func changelogSection(path, version string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open changelog: %w", err)
	}

	headerPrefix := "## [" + version + "]"
	var lines []string
	found := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !found {
			found = strings.HasPrefix(line, headerPrefix)
			continue
		}
		if strings.HasPrefix(line, "## ") {
			break
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("read changelog: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close changelog: %w", err)
	}
	notes := strings.TrimSpace(strings.Join(lines, "\n"))
	if !found || notes == "" {
		return "", fmt.Errorf("changelog has no notes for release %s", version)
	}
	return notes, nil
}

func loadState(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]string), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read release announcement state: %w", err)
	}
	state := make(map[string]string)
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode release announcement state: %w", err)
	}
	return state, nil
}

func saveState(path string, state map[string]string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	file, err := os.CreateTemp(dir, ".release-announcements-*")
	if err != nil {
		return fmt.Errorf("create temporary state: %w", err)
	}
	tempPath := file.Name()
	defer func() { _ = os.Remove(tempPath) }()

	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("protect temporary state: %w", err)
	}
	if err := json.NewEncoder(file).Encode(state); err != nil {
		_ = file.Close()
		return fmt.Errorf("encode release announcement state: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync release announcement state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close release announcement state: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace release announcement state: %w", err)
	}
	return nil
}
