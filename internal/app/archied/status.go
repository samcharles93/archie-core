package archied

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"text/tabwriter"
	"time"

	"github.com/samcharles93/archie-core/internal/app/servicekit"
	"github.com/samcharles93/archie-core/internal/domain/presence"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/secret"
)

// RunStatus prints one line per service from the State Store's presence
// records. Exit codes: 0 every service up, 1 any service down or degraded or
// the State Store unreachable, 2 usage error.
func RunStatus(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("archied status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	cfgPath := flags.String("config", configuration.DefaultConfigPath(), "path to a TOML/YAML config file or configuration directory")
	overlayPath := flags.String("config-overlay", "", "path to a TOML/YAML overlay file or configuration directory applied on top of -config")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	_, doc, err := servicekit.Resolve(slog.New(slog.DiscardHandler), *cfgPath, *overlayPath)
	if err != nil {
		fmt.Fprintf(stderr, "archied status: %v\n", err)
		return 1
	}
	client, cleanup, err := servicekit.StateStoreClient(doc.Config.Services, secret.NewRegistry())
	if err != nil {
		fmt.Fprintf(stderr, "archied status: %v\n", err)
		return 1
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	records, err := client.ListPresence(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "archied status: state store unreachable: %v\n", err)
		return 1
	}
	now := time.Now()
	healthy := true
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tSTATE\tVERSION\tUPTIME\tDETAIL")
	for _, service := range presence.Mesh(records, now) {
		if service.State != presence.StateUp {
			healthy = false
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", service.Service, service.State, orDash(service.Version), uptime(service, now), service.Detail)
	}
	_ = w.Flush()
	if !healthy {
		return 1
	}
	return 0
}

func uptime(service presence.Service, now time.Time) string {
	if service.State == presence.StateDown {
		return "-"
	}
	return now.Sub(service.StartedAt).Round(time.Second).String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
