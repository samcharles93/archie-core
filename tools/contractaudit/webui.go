package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// webUISurface reports HTTP route coverage for the dashboard API. The contract
// of record is the `registerXRoutes` call sites in internal/webui, and the
// consumers are the path literals the dashboard's client actually calls.
//
// This is the one surface with no schema at all: 52 routes and their response
// shapes are hand-matched against ui/src, so it is also the one that needs the
// check the most. The extractor fails loudly rather than under-reporting -- see
// scanFile -- because a call shape it cannot parse would otherwise make every
// route it feeds look unconsumed, and a false finding in a gate is worse than
// no gate.
type webUISurface struct {
	root string
}

func newWebUISurface(root string) Surface { return &webUISurface{root: root} }

func (s *webUISurface) Name() string { return "internal/webui" }

// routePattern matches a route registration in internal/webui. Every route is
// registered as HandleFunc("METHOD /path", handler).
var routePattern = regexp.MustCompile(`(?:HandleFunc|Handle)\(\s*"([A-Z]+)\s+(/[^"]*)"`)

// routeLiteralPattern finds a const holding a non-literal route, so a route
// built from a constant is still seen. Without this, captureIntakeRoute would
// read as an unconsumed route when it is in fact a mounted one.
var routeLiteralPattern = regexp.MustCompile(`(?m)^\s*(?:const\s+)?([A-Za-z0-9_]+)\s*=\s*"([A-Z]+)\s+(/[^"]*)"`)

// Consumer call shapes. Go's regexp has no backreferences, so each quoting
// style gets its own alternative rather than a backreference pair.
//
// EventSource counts as a consumer: /events and /api/logs/stream are
// subscribed to, not fetched, and an extractor that only looks for fetch
// reports them unconsumed and is simply wrong.
var (
	fetchPathPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?:req|fetch)\(\s*"([^"]+)"`),
		regexp.MustCompile("(?:req|fetch)\\(\\s*`([^`]+)`"),
	}
	eventPathPatterns = []*regexp.Regexp{
		regexp.MustCompile(`new\s+EventSource\(\s*"([^"]+)"`),
		regexp.MustCompile("new\\s+EventSource\\(\\s*`([^`]+)`"),
	}
	// A bare path literal, so a call routed through a helper this extractor
	// does not know about is still credited.
	pathLiteralPattern = regexp.MustCompile(`"(/[a-zA-Z0-9_./{}:-]*)"`)

	// Every fetch/req/EventSource call's opening, captured with its first
	// argument, so the extractor can require that argument to be a literal
	// rather than hoping a shape it does not recognise never appears.
	callOpenPattern = regexp.MustCompile(`\b(?:req|fetch|new\s+EventSource)\s*\(\s*([^\s)\n]{0,80})`)
	// A declaration, not a call: `async function req(path) {`.
	declPattern = regexp.MustCompile(`\bfunction\s+(?:req|fetch)\s*\(`)
	// The wrapper delegating on a variable: `fetch(path, {`. This carries no
	// path by construction, so it is not an under-report.
	delegatePattern = regexp.MustCompile(`\b(?:req|fetch)\s*\(\s*[a-z][A-Za-z0-9_]*\s*[,)]`)
	// The first argument opening with a quote, which is what makes it a
	// literal path this extractor must have extracted.
	literalArgOnly = regexp.MustCompile("^[\"`]")
	// An EventSource subscription built from a variable is skipped for the
	// same reason as the wrapper delegation above.

	tmpPattern       = regexp.MustCompile(`\$\{[^}]*\}`)
	routeParamPat    = regexp.MustCompile(`\{[^}]+\}`)
	apiPathPrefix    = "/api/"
	eventStreamRoute = "/events"
)

func (s *webUISurface) Subjects() ([]Subject, error) {
	serverPath := filepath.Join(s.root, "internal/webui/server.go")
	raw, err := os.ReadFile(serverPath)
	if err != nil {
		return nil, fmt.Errorf("read webui routes: %w", err)
	}
	serverSource := string(raw)

	// Resolve route constants before scanning registrations.
	for _, match := range routeLiteralPattern.FindAllStringSubmatch(serverSource, -1) {
		serverSource = strings.ReplaceAll(serverSource, match[1], `"`+match[2]+`"`)
	}

	var routes []Subject
	seen := map[string]bool{}
	for _, match := range routePattern.FindAllStringSubmatch(serverSource, -1) {
		method, path := match[1], normalizeRoute(match[2])
		// The surface is the dashboard API contract: the routes the bundled
		// client consumes. The asset mount and the health probes have
		// different consumers -- a browser and a load balancer or the update
		// watchdog -- so they are out of scope here rather than reported as
		// undeclared. Reporting them would be a false finding, and a false
		// finding in a gate is worse than no gate.
		if !strings.HasPrefix(path, apiPathPrefix) && path != eventStreamRoute {
			continue
		}
		name := method + " " + path
		if seen[name] {
			continue
		}
		seen[name] = true
		routes = append(routes, Subject{Name: name, Evidence: "internal/webui/server.go"})
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("parsed 0 routes from internal/webui/server.go; the parser is stale, refusing to report an empty surface")
	}

	consumed, err := s.consumerPaths()
	if err != nil {
		return nil, err
	}
	for i := range routes {
		if consumer, ok := consumed[routes[i].Name]; ok {
			routes[i].Consumed = true
			routes[i].Consumer = consumer
		}
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Name < routes[j].Name })
	return routes, nil
}

// consumerPaths walks the dashboard source for path literals and maps each to
// a normalized "METHOD /path" route name. A path is credited to both GET and
// POST, because a bare literal cannot tell one verb from the other and
// reporting a route unconsumed on a verb mismatch would be a false finding.
func (s *webUISurface) consumerPaths() (map[string]string, error) {
	srcDir := filepath.Join(s.root, "ui/src")
	consumed := map[string]string{}

	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || (filepath.Ext(path) != ".jsx" && filepath.Ext(path) != ".js") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		literals, err := scanFile(rel, string(body))
		if err != nil {
			return err
		}
		for _, literal := range literals {
			if !strings.HasPrefix(literal, apiPathPrefix) && literal != eventStreamRoute {
				continue
			}
			route := normalizeRoute(literal)
			for _, method := range []string{"GET", "POST", "PATCH", "DELETE"} {
				key := method + " " + route
				if _, exists := consumed[key]; !exists {
					consumed[key] = rel
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk dashboard source: %w", err)
	}
	if len(consumed) == 0 {
		return nil, fmt.Errorf("found 0 dashboard API call sites; refusing to report every route as unconsumed")
	}
	return consumed, nil
}

// scanFile returns every API path literal in one dashboard source file.
//
// It fails loudly on a call it cannot parse. Under-reporting is how a gate
// silently stops checking, so a `fetch`/`req`/`EventSource` whose first
// argument is neither a literal nor one of the two shapes that legitimately
// carries no path -- a declaration, or the wrapper delegating on a variable --
// is an error naming the file rather than a quietly smaller consumer set.
func scanFile(rel, body string) ([]string, error) {
	var literals []string
	for _, pattern := range append(append([]*regexp.Regexp{}, fetchPathPatterns...), eventPathPatterns...) {
		for _, match := range pattern.FindAllStringSubmatch(body, -1) {
			literals = append(literals, match[1])
		}
	}
	for _, match := range pathLiteralPattern.FindAllStringSubmatch(body, -1) {
		literals = append(literals, match[1])
	}

	// Every call whose first argument is not a literal, minus the declarations
	// and variable delegations that legitimately produce no path.
	//
	// The guard is deliberately one-directional. There is no matching "found no
	// calls" condition: a file may legitimately carry a path literal with no
	// call of its own (a router table, a link), and the aggregate check in
	// consumerPaths already fails when the whole surface yields nothing.
	declarations := len(declPattern.FindAllString(body, -1))
	delegations := len(delegatePattern.FindAllString(body, -1))
	for _, match := range callOpenPattern.FindAllStringSubmatch(body, -1) {
		first := match[1]
		if first == "" || literalArgOnly.MatchString(first) {
			continue
		}
		// A non-literal first argument. Accounted for by a declaration or a
		// delegation, or an under-report.
		if declarations+delegations > 0 {
			declarations--
			continue
		}
		return nil, fmt.Errorf(
			"%s: fetch/req/EventSource call with non-literal first argument %q; the extractor cannot see the path it builds and would under-report",
			rel, first,
		)
	}
	return literals, nil
}

// normalizeRoute strips query strings and replaces both `${...}` template
// interpolations and `{param}` wildcards with a single placeholder, so a
// parameter's *name* never causes a false mismatch. `{id}` in the server and
// `{taskId}` in the client are the same route.
func normalizeRoute(route string) string {
	route = strings.SplitN(route, "?", 2)[0]
	route = tmpPattern.ReplaceAllString(route, "{}")
	route = routeParamPat.ReplaceAllString(route, "{}")
	return route
}
