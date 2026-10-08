package webui

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/presence"
	"github.com/samcharles93/archie-core/internal/logging"
)

// LogSource reads one service's bounded recent log over its contract.
type LogSource func(context.Context, logging.Query) (logging.Result, error)

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := logging.DefaultTailLines
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			http.Error(w, "bad limit", http.StatusBadRequest)
			return
		}
		if parsed > 0 {
			limit = min(parsed, logging.MaxTailLines)
		}
	}
	sources := s.LogSources
	if len(sources) == 0 {
		sources = map[string]LogSource{presence.UI: func(_ context.Context, q logging.Query) (logging.Result, error) { return s.LogFeed.Read(q) }}
	}
	service := q.Get("service")
	owner := service
	if service == presence.Agent || service == presence.Broker {
		owner = presence.Daemon
	}
	if owner != "" && sources[owner] == nil {
		http.Error(w, "unknown service", http.StatusBadRequest)
		return
	}
	query := logging.Query{Limit: limit, Levels: splitCSV(q.Get("level")), Component: q.Get("component"), Contains: q.Get("q")}
	entries, errors, truncated := readServiceLogs(r.Context(), sources, owner, query)
	services := logServices(sources, entries)
	components := mergeComponents(nil, entries)
	if service != "" {
		entries = slices.DeleteFunc(entries, func(e logging.Entry) bool { return e.Fields["service"] != service })
	}
	slices.SortStableFunc(entries, func(a, b logging.Entry) int { return a.Time.Compare(b.Time) })
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
		truncated = true
	}
	writeJSON(w, map[string]any{"entries": entries, "components": components, "services": services, "errors": errors, "truncated": truncated, "disabled": len(s.LogSources) == 0 && s.LogFeed == nil})
}

func readServiceLogs(ctx context.Context, sources map[string]LogSource, selected string, q logging.Query) ([]logging.Entry, map[string]string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	entries := []logging.Entry{}
	failures := map[string]string{}
	truncated := false
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, source := range sources {
		if selected != "" && name != selected {
			continue
		}
		wg.Go(func() {
			result, err := source(ctx, q)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures[name] = err.Error()
				return
			}
			truncated = truncated || result.Truncated
			for _, entry := range result.Entries {
				entry.Fields = maps.Clone(entry.Fields)
				if entry.Fields == nil {
					entry.Fields = map[string]any{}
				}
				// Only the daemon receives worker and embedded-broker logs.
				observed, _ := entry.Fields["service"].(string)
				if name != presence.Daemon || (observed != presence.Agent && observed != presence.Broker) {
					entry.Fields["service"] = name
				}
				entries = append(entries, entry)
			}
		})
	}
	wg.Wait()
	return entries, failures, truncated
}

func mergeComponents(history []string, live []logging.Entry) []string {
	seen := make(map[string]struct{}, len(history))
	for _, component := range history {
		seen[component] = struct{}{}
	}
	for _, entry := range live {
		if component, ok := entry.Fields["component"].(string); ok && component != "" {
			seen[component] = struct{}{}
		}
	}
	components := make([]string, 0, len(seen))
	for component := range seen {
		components = append(components, component)
	}
	slices.Sort(components)
	return components
}

func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func logServices(sources map[string]LogSource, entries []logging.Entry) []string {
	services := make([]string, 0, len(sources))
	for name := range sources {
		services = append(services, name)
	}
	for _, entry := range entries {
		if name, ok := entry.Fields["service"].(string); ok && !slices.Contains(services, name) {
			services = append(services, name)
		}
	}
	slices.Sort(services)
	return services
}
