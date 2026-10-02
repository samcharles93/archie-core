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

// ShippedSoul returns the SOUL document this build ships.
//
// Exactly one body is shipped, so the seeder recognises only this text as
// untouched scaffolding. A build that supersedes it adds the old body next to
// this one together with the in-place upgrade that consumes it; a branch that
// rewrites a file for matching a template no build ever shipped has no
// producer and must not be kept ahead of one.
func ShippedSoul() string {
	return DefaultSoul
}

// SoulMatchesShipped reports whether content is the same body as shipped, once
// transport noise is normalised away.
//
// Comparison is on normalised content, so a CRLF checkout or an editor that
// left trailing whitespace still matches the template it came from. Only a
// body that is identical to a shipped document after that normalisation is
// eligible for replacement; any other difference is treated as user intent.
func SoulMatchesShipped(content, shipped string) bool {
	return normalizeSoul(content) == normalizeSoul(shipped)
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
