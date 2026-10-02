package agent

import "strings"

// DefaultSoul is the starter SOUL document this build ships.
//
// It is deliberately independent of ShippedPersonas: the persona catalogue is
// a style axis that is being retired, while the starter SOUL is the fallback
// identity a user edits. Deleting personas must not delete the fallback.
//
// The document is plain Markdown and becomes the identity slot of the system
// prompt, XML-escaped beneath the invariant sections. It owns the agent's
// name, register and warmth only; it cannot change the rules, tools or runtime
// metadata, so keep it to identity prose.
const DefaultSoul = `# Archie

You are Archie, a capable coding and project assistant. Be direct without being
brusque, grounded in the actual workspace state, and use available tools when
asked. Do not claim tools, files, memories, actions, or results you have not
verified. Do not impersonate another assistant, provider, or vendor.
`

// SoulMatch classifies a SOUL body against the documents a build ships.
type SoulMatch int

const (
	// SoulEdit is content no build shipped: a user edit, which must never be
	// replaced.
	SoulEdit SoulMatch = iota
	// SoulCurrent is content identical to the build's Default once transport
	// noise is normalised away; there is nothing to upgrade.
	SoulCurrent
	// SoulSuperseded is a body this build recognises as a shipped template
	// that is not the current Default. It carries zero user intent, so it may
	// be upgraded in place.
	SoulSuperseded
)

// SoulDocument is the set of SOUL bodies one build recognises as its own.
type SoulDocument struct {
	// Default is written when no SOUL file exists, and replaces a superseded
	// template during an upgrade.
	Default string
	// Legacy lists earlier shipped SOUL bodies this build still recognises. A
	// file that matches one is an untouched scaffolding document rather than a
	// user edit. The Default is always recognised and need not be repeated.
	Legacy []string
}

// ShippedSoul returns the SOUL document this build ships.
func ShippedSoul() SoulDocument {
	return SoulDocument{Default: DefaultSoul}
}

// Match classifies a body against the build's shipped documents.
//
// Comparison is on normalised content, so a CRLF checkout or an editor that
// left trailing whitespace still matches the template it came from. Only a
// body that is identical to a shipped document after that normalisation is
// eligible for replacement; any other difference is treated as user intent.
func (d SoulDocument) Match(content string) SoulMatch {
	if d.Default == "" {
		return SoulEdit
	}
	normalized := normalizeSoul(content)
	if normalized == normalizeSoul(d.Default) {
		return SoulCurrent
	}
	for _, legacy := range d.Legacy {
		if legacy == "" {
			continue
		}
		if normalized == normalizeSoul(legacy) {
			return SoulSuperseded
		}
	}
	return SoulEdit
}

// normalizeSoul reduces a SOUL body to the content a user authored: a UTF-8
// BOM is dropped, CRLF and lone CR become LF, and trailing whitespace is
// removed from every line and from the end of the document. None of that
// carries intent, so it must not make an untouched template look edited.
func normalizeSoul(content string) string {
	content = strings.TrimPrefix(content, "\ufeff")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}
