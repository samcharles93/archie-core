package archied

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

// RunUpdate serves `archied update check`, printing the release snapshot as
// JSON. The channel defaults to the environment the installer passes on.
func RunUpdate(args []string, out, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprintln(stderr, "usage: archied update check [-channel stable|next|exact-pin] [-pin version]")
		return 2
	}
	flags := flag.NewFlagSet("update check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	channel := flags.String("channel", envOr("ARCHIE_UPDATE_RELEASE_CHANNEL", "stable"), "stable, next, or exact-pin")
	pin := flags.String("pin", os.Getenv("ARCHIE_UPDATE_PIN"), "exact release version")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	catalog := releaseupdate.GitHubCatalog{Channel: func(context.Context) (string, string, error) { return *channel, *pin, nil }}
	snapshot, err := catalog.Check(context.Background())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(snapshot); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
