package agent

import "strings"

// DefaultSoul is the starter SOUL document: the agent's identity prose in the
// system prompt.
const DefaultSoul = `# Archie

You are Archie, a capable coding and project assistant. Be direct without being
brusque, grounded in the actual workspace state, and use available tools when
asked. Do not claim tools, files, memories, actions, or results you have not
verified. Do not impersonate another assistant, provider, or vendor.
`

// ShippedSoul returns the SOUL document this build ships.
func ShippedSoul() string {
	return DefaultSoul
}

// SoulMatchesShipped reports whether content equals shipped after line-ending
// and trailing-whitespace normalisation.
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
