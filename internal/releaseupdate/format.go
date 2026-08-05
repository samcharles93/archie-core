package releaseupdate

import (
	"fmt"
	"strings"
)

// FormatSnapshot returns the concise human-facing update status shared by
// chat adapters. It deliberately includes unchanged components when an
// update exists so operators can see exactly what will and will not change.
func FormatSnapshot(snapshot Snapshot) string {
	if len(snapshot.Available()) == 0 && !snapshot.Deferred {
		return "Archie is up to date."
	}
	if len(snapshot.Available()) == 0 && snapshot.Deferred {
		return "The available update is deferred. Archie will ask again when a newer release is available."
	}
	var text strings.Builder
	text.WriteString("Archie has an update available:")
	for _, component := range snapshot.Components {
		fmt.Fprintf(&text, "\n\n--- %s ---\n", component.Label)
		if component.Available == "" || component.Available == component.Installed {
			fmt.Fprintf(&text, "No %s changes as part of this release.", strings.ToLower(strings.TrimPrefix(component.Label, "THE ")))
			continue
		}
		fmt.Fprintf(&text, "%s available", component.Available)
		if component.Changelog != "" {
			fmt.Fprintf(&text, "\n\n%s", component.Changelog)
		}
	}
	return text.String()
}
