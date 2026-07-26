package telegram

import "strings"

import "testing"

func TestMarkdownToHTML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text untouched", in: "hello there", want: "hello there"},
		{name: "bold", in: "here's what I **can** do", want: "here's what I <b>can</b> do"},
		{name: "underscore bold", in: "__strong__", want: "<b>strong</b>"},
		{name: "italic", in: "*emphasis*", want: "<i>emphasis</i>"},
		{name: "strikethrough", in: "~~gone~~", want: "<s>gone</s>"},
		{name: "inline code", in: "the `/commands` system", want: "the <code>/commands</code> system"},
		{name: "link", in: "[docs](https://x.test)", want: `<a href="https://x.test">docs</a>`},
		{name: "heading becomes bold", in: "## Capabilities", want: "<b>Capabilities</b>"},
		{name: "bullet becomes dot", in: "- Answer questions", want: "• Answer questions"},
		{
			name: "html in user text is escaped, not injected",
			in:   "use <script>alert(1)</script> & co",
			want: "use &lt;script&gt;alert(1)&lt;/script&gt; &amp; co",
		},
		{
			name: "markdown inside code is left alone",
			in:   "`**not bold**`",
			want: "<code>**not bold**</code>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := markdownToHTML(tt.in); got != tt.want {
				t.Errorf("markdownToHTML(%q)\n got: %q\nwant: %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMarkdownToHTMLFencedCode(t *testing.T) {
	got := markdownToHTML("run:\n```go\nfmt.Println(1 < 2)\n```")
	want := "run:\n" + `<pre><code class="language-go">fmt.Println(1 &lt; 2)</code></pre>`
	if got != want {
		t.Errorf("fenced code\n got: %q\nwant: %q", got, want)
	}
}

// Telegram has no table element, so a Markdown table must become a <pre>
// block or it arrives as unreadable pipe soup. Regression for the real
// LLM reply that prompted this.
func TestMarkdownToHTMLTableBecomesPre(t *testing.T) {
	in := "| Capability | Description |\n|---|---|\n| **Code** | Write, review |"
	got := markdownToHTML(in)
	if !strings.HasPrefix(got, "<pre>") || !strings.HasSuffix(got, "</pre>") {
		t.Fatalf("table not wrapped in <pre>: %q", got)
	}
	if strings.Contains(got, "<b>") {
		t.Errorf("table interior must stay literal, got %q", got)
	}
}

// Every emitted tag must be one Telegram actually accepts.
func TestMarkdownToHTMLEmitsOnlySupportedTags(t *testing.T) {
	in := "# H\n\nsome **b** and *i* and `c`\n\n- item\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```sh\nls\n```"
	got := markdownToHTML(in)
	for _, bad := range []string{"<p>", "<ul>", "<li>", "<h1>", "<table>", "<tr>", "<td>"} {
		if strings.Contains(got, bad) {
			t.Errorf("emitted unsupported tag %s: %q", bad, got)
		}
	}
}
