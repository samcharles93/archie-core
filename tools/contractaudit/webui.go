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

// Consumer call shapes.
//
// Extraction is deliberately name-agnostic: it matches the path *literal*
// rather than the call that carries it. An earlier version matched `req|fetch`
// by function name, and rewriting the client's helper from `req(` to
// `request(` silently reported 16 live routes as unconsumed. The contract this
// surface audits is "the dashboard calls this path", and a literal is how the
// dashboard expresses that regardless of what the surrounding call is called.
var (
	// pathLiteralPattern matches a path literal that is the first argument of a
	// call, in either quoting style. Both are needed: a fixed path is written
	// with double quotes and an interpolated one with backticks.
	//
	// Two properties are load-bearing, and they pull in opposite directions.
	//
	// It does not name the function, because naming it broke on rename: matching
	// `req|fetch` reported 16 live routes as unconsumed the moment the
	// dashboard's helper became `request(`.
	//
	// It requires a call, because dropping *that* requirement made the check
	// vacuous: crediting any quoted `/api/...` literal meant a comment, a dead
	// array, or a display label counted as proof that the route was consumed, so
	// deleting every real request still read as healthy. The two directions are
	// not symmetric -- a false "unconsumed" is a loud, fixable failure, while a
	// false "consumed" is a silent hole in the gate, which is worse than having
	// no gate at all.
	//
	// Known limit: the path must be the *first* argument. Accepting a comma
	// before the literal would start crediting array elements, restoring the
	// vacuity this pattern exists to prevent.
	pathLiteralPattern = regexp.MustCompile("\\b[A-Za-z_$][A-Za-z0-9_$]*\\s*\\(\\s*[\"`](/(?:api/|events)[^\"`\\s]*)")

	// fragmentPattern matches a literal that begins an API path but does not
	// hold the whole route, e.g. `"/api" + "/tasks"`. Such a route cannot be
	// credited, so the file is reported rather than silently contributing
	// nothing -- under-reporting is how a gate stops checking unnoticed.
	fragmentPattern = regexp.MustCompile("[\"`](/api)[\"`]")

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

// scanFile returns every request path in one dashboard source file.
//
// Two things must be true of a mention before it counts as a consumer: it must
// be the first argument of a call, and it must be code rather than prose. The
// first excludes bare constants, dead arrays, and object properties; the second
// excludes a commented-out call, which otherwise leaves a call-shaped path in
// the file and reads as live.
//
// The only failure it reports is a path it cannot see whole. That is a narrow
// guard rather than a heuristic: it fires when a file concatenates a bare /api
// fragment into a route, because no literal then holds the path and the
// extractor would otherwise contribute nothing for that route without saying so.
func scanFile(rel, body string) ([]string, error) {
	code := stripJSComments(body)
	if fragmentPattern.MatchString(code) {
		return nil, fmt.Errorf(
			"%s: builds an API path from a bare /api fragment; the extractor cannot see the whole route and would under-report",
			rel,
		)
	}
	var literals []string
	for _, match := range pathLiteralPattern.FindAllStringSubmatch(code, -1) {
		literals = append(literals, match[1])
	}
	return literals, nil
}

// stripJSComments removes // and /* */ comments while leaving string, template,
// and regex literals intact, so the extractor measures code rather than prose
// about code. Without it, commenting out a call still reads as a live consumer
// -- the same silent hole as crediting a bare literal.
//
// Known limit: template-literal interpolations are treated as string content, so
// a request written inside `${...}` is not seen. No such call exists in ui/src.
func stripJSComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	var quote byte
	for i := 0; i < len(src); {
		c := src[i]
		var next byte
		if i+1 < len(src) {
			next = src[i+1]
		}
		if quote != 0 {
			b.WriteByte(c)
			if c == '\\' && i+1 < len(src) {
				b.WriteByte(next)
				i += 2
				continue
			}
			if c == quote {
				quote = 0
			}
			i++
			continue
		}
		switch {
		case c == '"' || c == '\'' || c == '`':
			quote = c
			b.WriteByte(c)
			i++
		case c == '/' && next == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && next == '*':
			i += 2
			for i < len(src) && !(src[i] == '*' && i+1 < len(src) && src[i+1] == '/') {
				i++
			}
			i += 2
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
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
