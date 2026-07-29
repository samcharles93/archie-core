// Package command inspects shell command lines before they are executed.
//
// It exists because a substring search over a command line is wrong in both
// directions. `git commit -m "revert the rm -rf change"` is harmless and
// would be blocked; `echo x; rm -rf /` is not and, with a naive anchor on
// the start of the line, would be allowed. Deciding either way requires
// knowing where a command actually begins, which means parsing quoting and
// separators rather than pattern-matching the raw text.
//
// Tool execution owns command constraints (see docs/architecture/policy.md).
// When internal/policy's evaluation API lands, [Hardline] becomes an
// evaluator registered there; the rules themselves stay here.
package command

import "strings"

// Segment is one command invocation found within a command line, already
// split from its neighbours and stripped of quoting.
type Segment struct {
	// Raw is the segment's original text, for reporting.
	Raw string
	// Words are the segment's tokens with quoting removed, including any
	// privilege wrapper such as sudo.
	Words []string
	// Name is the command being invoked: the first word after leading
	// environment assignments and privilege wrappers have been skipped.
	// Empty when the segment invokes nothing.
	Name string
	// Args are the words following Name.
	Args []string
	// Elevated reports whether the segment runs through sudo or doas.
	Elevated bool
	// ElevationArgs are the flags given to that privilege wrapper.
	ElevationArgs []string
}

// Split breaks a command line into the individual commands within it.
//
// Separators are only honoured outside quotes, which is what keeps a
// quoted argument from being read as a new command: in
// `echo "a; rm -rf /"` the semicolon is data, so the whole line is one
// segment whose command is echo. Command substitutions and subshells do
// start new commands, so `$(rm -rf /)` and `(rm -rf /)` are seen.
func Split(line string) []Segment {
	var (
		segments []Segment
		current  strings.Builder
		quote    rune
		escaped  bool
		prev     rune
		// varBraces counts open ${...} expansions. Their braces are part
		// of a word, unlike the braces of a { cmd; } group, so they must
		// not split the line -- otherwise ${HOME} parses as the separate
		// pieces "$", "HOME" and the rest.
		varBraces int
	)

	flush := func() {
		if seg, ok := newSegment(current.String()); ok {
			segments = append(segments, seg)
		}
		current.Reset()
	}

	runes := []rune(line)
	for i := range runes {
		r := runes[i]

		if escaped {
			current.WriteRune(r)
			escaped = false
			prev = r
			continue
		}
		// Inside single quotes a backslash is literal, so escaping only
		// applies elsewhere.
		if r == '\\' && quote != '\'' {
			current.WriteRune(r)
			escaped = true
			prev = r
			continue
		}

		if quote != 0 {
			current.WriteRune(r)
			if r == quote {
				quote = 0
			}
			prev = r
			continue
		}
		if r == '\'' || r == '"' {
			current.WriteRune(r)
			quote = r
			prev = r
			continue
		}

		// ${...} is one word, not a brace group.
		if r == '{' && prev == '$' {
			varBraces++
			current.WriteRune(r)
			prev = r
			continue
		}
		if r == '}' && varBraces > 0 {
			varBraces--
			current.WriteRune(r)
			prev = r
			continue
		}

		// Unquoted from here: these end the current command and begin
		// another. Backtick and the bracket forms are included because a
		// substitution, subshell, or brace group contains commands of its
		// own -- `echo $(rm -rf /)` must be seen as an rm.
		if strings.ContainsRune(";&|\n()`{}", r) {
			flush()
			prev = r
			continue
		}
		current.WriteRune(r)
		prev = r
	}
	flush()

	return segments
}

// newSegment tokenises one segment, reporting false when it invokes
// nothing.
func newSegment(raw string) (Segment, bool) {
	return segmentFromWords(strings.TrimSpace(raw), tokenise(raw))
}

// segmentFromWords builds a segment from already-tokenised words. It is
// shared with wrapper unwrapping, where the inner command is a slice of an
// outer segment's words and re-parsing text would lose the original
// quoting.
func segmentFromWords(raw string, words []string) (Segment, bool) {
	if len(words) == 0 {
		return Segment{}, false
	}

	seg := Segment{Raw: raw, Words: words}

	// Leading NAME=value pairs set the environment for the command that
	// follows rather than being the command, so `FOO=bar rm -rf /` is an
	// rm invocation.
	i := 0
	for i < len(words) && isAssignment(words[i]) {
		i++
	}

	// A privilege wrapper likewise fronts the real command. Its own flags
	// are kept, because some of them are the thing worth refusing.
	if i < len(words) && (words[i] == "sudo" || words[i] == "doas") {
		seg.Elevated = true
		i++
		for i < len(words) && strings.HasPrefix(words[i], "-") {
			seg.ElevationArgs = append(seg.ElevationArgs, words[i])
			i++
		}
		// sudo VAR=value cmd is accepted by sudo too.
		for i < len(words) && isAssignment(words[i]) {
			i++
		}
	}

	if i >= len(words) {
		// Nothing but assignments or a bare sudo: no command to judge,
		// but the elevation flags may still matter.
		return seg, seg.Elevated
	}

	seg.Name = words[i]
	seg.Args = words[i+1:]
	return seg, true
}

// tokenise splits a segment into words, removing quotes and treating
// redirection operators as words of their own so that a redirect target
// can be examined.
func tokenise(raw string) []string {
	var (
		words   []string
		current strings.Builder
		quote   rune
		escaped bool
		filled  bool
	)

	end := func() {
		if filled {
			words = append(words, current.String())
			current.Reset()
			filled = false
		}
	}

	for _, r := range raw {
		if escaped {
			current.WriteRune(r)
			filled = true
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}

		if consumed := consumeQuote(r, &quote, &current, &filled); consumed {
			continue
		}

		switch r {
		case ' ', '\t', '\r':
			end()
		case '>', '<':
			end()
			words = append(words, string(r))
		default:
			current.WriteRune(r)
			filled = true
		}
	}
	end()

	return words
}

// consumeQuote handles one rune's interaction with quoting, reporting
// whether it was consumed. Quote characters themselves are dropped: the
// caller wants an argument's value, so that "/" and / compare equal.
func consumeQuote(r rune, quote *rune, current *strings.Builder, filled *bool) bool {
	if *quote != 0 {
		if r == *quote {
			*quote = 0
			return true
		}
		current.WriteRune(r)
		*filled = true
		return true
	}
	if r == '\'' || r == '"' {
		*quote = r
		// An empty quoted string is still a word.
		*filled = true
		return true
	}
	return false
}

// isAssignment reports whether a word is a NAME=value environment
// assignment rather than a command.
func isAssignment(word string) bool {
	eq := strings.Index(word, "=")
	if eq <= 0 {
		return false
	}
	for i, r := range word[:eq] {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
		isDigit := r >= '0' && r <= '9'
		if !isLetter && (!isDigit || i == 0) {
			return false
		}
	}
	return true
}
