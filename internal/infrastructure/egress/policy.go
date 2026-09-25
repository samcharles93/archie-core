package egress

import (
	"net"
	"strconv"
	"strings"

	"github.com/docker/sandbox-kit-spec/v3/spec"
)

// pattern is one network-policy@1 entry: an exact host, a host on one port,
// a single-label wildcard, or everything.
type pattern struct {
	everything bool
	suffix     string // ".example.com" for "*.example.com"
	host       string
	port       int // 0 means any port
}

// rules is a compiled allow/deny pair for one phase. Deny wins; an empty or
// absent phase allows nothing, so egress is closed unless a Kit asks for it.
type rules struct {
	allow []pattern
	deny  []pattern
}

func compileRules(r *spec.NetworkRules) rules {
	if r == nil {
		return rules{}
	}
	return rules{allow: compilePatterns(r.Allow), deny: compilePatterns(r.Deny)}
}

func compilePatterns(entries []string) []pattern {
	out := make([]pattern, 0, len(entries))
	for _, e := range entries {
		out = append(out, compilePattern(e))
	}
	return out
}

func compilePattern(entry string) pattern {
	entry = strings.TrimSpace(entry)
	if entry == "*" || entry == "**" {
		return pattern{everything: true}
	}
	var p pattern
	if h, portText, err := net.SplitHostPort(entry); err == nil {
		if port, err := strconv.Atoi(portText); err == nil {
			entry, p.port = h, port
		}
	}
	entry = normalizeHost(entry)
	if rest, ok := strings.CutPrefix(entry, "*."); ok {
		p.suffix = "." + rest
		return p
	}
	p.host = entry
	return p
}

func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func (p pattern) matches(host string, port int) bool {
	if p.everything {
		return true
	}
	if p.port != 0 && p.port != port {
		return false
	}
	if p.suffix != "" {
		label, ok := strings.CutSuffix(host, p.suffix)
		return ok && label != "" && !strings.Contains(label, ".") && net.ParseIP(host) == nil
	}
	return host == p.host
}

func (r rules) allows(host string, port int) bool {
	host = normalizeHost(host)
	for _, p := range r.deny {
		if p.matches(host, port) {
			return false
		}
	}
	for _, p := range r.allow {
		if p.matches(host, port) {
			return true
		}
	}
	return false
}
