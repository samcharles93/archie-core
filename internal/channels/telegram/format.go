package telegram

import (
	"regexp"
	"strings"
)

// Telegram's Bot API accepts a small HTML subset for message formatting:
// <b> <i> <u> <s> <a> <code> <pre> <blockquote> <tg-spoiler>. It has no
// notion of headings, bullets, or tables. LLM replies, however, are plain
// Markdown  --  so without conversion the raw syntax (**bold**, backticks,
// pipe tables) is shown to the user verbatim.
//
// markdownToHTML converts the Markdown subset an LLM realistically emits
// into that HTML subset. It is deliberately conservative: anything it
// cannot map is escaped and passed through as literal text rather than
// guessed at, and sendMessage falls back to an unformatted send if
// Telegram still rejects the result.

var (
	// fencedBlockRe matches ```lang\n...\n``` code fences.
	fencedBlockRe = regexp.MustCompile("(?s)```([A-Za-z0-9_+-]*)\\n?(.*?)```")
	// inlineCodeRe matches `code` spans.
	inlineCodeRe = regexp.MustCompile("`([^`\n]+)`")
	// linkRe matches [text](url).
	linkRe = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	// boldRe matches **text** and __text__.
	boldRe = regexp.MustCompile(`\*\*([^*\n]+)\*\*|__([^_\n]+)__`)
	// italicRe matches *text* and _text_ (after bold has been consumed).
	italicRe = regexp.MustCompile(`\*([^*\n]+)\*|(?:^|[\s(])_([^_\n]+)_`)
	// strikeRe matches ~~text~~.
	strikeRe = regexp.MustCompile(`~~([^~\n]+)~~`)
	// headingRe matches leading #, ##, ... headings.
	headingRe = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.*)$`)
	// bulletRe matches -, * and + list markers.
	bulletRe = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	// tableSepRe matches a Markdown table separator row (|---|:--:|).
	tableSepRe = regexp.MustCompile(`^\s*\|?[\s:|-]*-[\s:|-]*\|?\s*$`)
	// hrRe matches a horizontal rule. RE2 has no backreferences, so each
	// marker character gets its own alternative rather than \1.
	hrRe = regexp.MustCompile(`^\s*(?:-\s*-\s*-[-\s]*|\*\s*\*\s*\*[*\s]*|_\s*_\s*_[_\s]*)$`)
)

// markdownToHTML converts LLM Markdown to the Telegram HTML subset.
func markdownToHTML(md string) string {
	// Code content must not be touched by any inline rule, so pull fenced
	// and inline code out first and re-insert after everything else.
	var code []string
	stash := func(s string) string {
		code = append(code, s)
		// \x00 cannot appear in a Telegram message, so it is a safe
		// placeholder sentinel.
		return "\x00" + itoa(len(code)-1) + "\x00"
	}

	md = fencedBlockRe.ReplaceAllStringFunc(md, func(m string) string {
		sub := fencedBlockRe.FindStringSubmatch(m)
		lang, body := sub[1], strings.TrimRight(sub[2], "\n")
		open := "<pre>"
		if lang != "" {
			open = `<pre><code class="language-` + escapeHTML(lang) + `">`
		}
		close := "</pre>"
		if lang != "" {
			close = "</code></pre>"
		}
		return stash(open + escapeHTML(body) + close)
	})
	md = inlineCodeRe.ReplaceAllStringFunc(md, func(m string) string {
		body := inlineCodeRe.FindStringSubmatch(m)[1]
		return stash("<code>" + escapeHTML(body) + "</code>")
	})

	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// A table needs monospace to stay aligned, and Telegram has no
		// table element  --  emit the whole block inside <pre>.
		if isTableRow(line) && i+1 < len(lines) && tableSepRe.MatchString(lines[i+1]) {
			var block []string
			for i < len(lines) && (isTableRow(lines[i]) || tableSepRe.MatchString(lines[i])) {
				block = append(block, strings.TrimSpace(lines[i]))
				i++
			}
			i--
			out = append(out, "<pre>"+escapeHTML(strings.Join(block, "\n"))+"</pre>")
			continue
		}

		if hrRe.MatchString(line) {
			out = append(out, "──────────")
			continue
		}
		if m := headingRe.FindStringSubmatch(line); m != nil {
			out = append(out, "<b>"+inline(m[1])+"</b>")
			continue
		}
		if m := bulletRe.FindStringSubmatch(line); m != nil {
			out = append(out, m[1]+"• "+inline(m[2]))
			continue
		}
		out = append(out, inline(line))
	}

	res := strings.Join(out, "\n")

	// Re-insert code, innermost placeholder first.
	for i, c := range code {
		res = strings.ReplaceAll(res, "\x00"+itoa(i)+"\x00", c)
	}
	return res
}

// inline applies the span-level rules to one line of already code-stripped
// text, escaping it first so user text can never inject markup.
func inline(s string) string {
	s = escapeHTML(s)
	s = linkRe.ReplaceAllString(s, `<a href="$2">$1</a>`)
	s = boldRe.ReplaceAllString(s, "<b>$1$2</b>")
	s = strikeRe.ReplaceAllString(s, "<s>$1</s>")
	s = italicRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := italicRe.FindStringSubmatch(m)
		body := sub[1] + sub[2]
		// Preserve the leading boundary char consumed by the _ branch.
		prefix := ""
		if sub[1] == "" && len(m) > 0 && m[0] != '_' {
			prefix = string(m[0])
		}
		return prefix + "<i>" + body + "</i>"
	})
	return s
}

// isTableRow reports whether a line looks like a Markdown table row.
func isTableRow(s string) bool {
	t := strings.TrimSpace(s)
	return strings.Count(t, "|") >= 2
}

// escapeHTML escapes the three characters Telegram's HTML parser treats as
// markup. Order matters: & must be escaped first.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// itoa is a tiny strconv.Itoa to keep the placeholder path allocation-free
// of the strconv import for such a narrow use.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
