package gateway

import (
	_ "embed"
	"strings"
	"text/template"
	"time"
)

//go:embed templates/archie.md.tpl
var archiePromptTpl string

// ToolSummary is the capability metadata for one registered tool, as it is
// advertised to the model.
type ToolSummary struct {
	Name        string
	Description string
}

// SystemPromptConfig holds the inputs for one rendered system prompt.
//
// Persona supplies identity and communication style and comes from the
// [PersonaRegistry]; every other field is runtime state the model cannot
// infer. The distinction matters: switching persona must not be able to
// remove the tool inventory or the instruction hierarchy, so those live in
// the template rather than in any persona string.
type SystemPromptConfig struct {
	// Persona is the active persona prompt, rendered first.
	Persona string
	// Tools is the complete set of tools available for this turn. An empty
	// slice is meaningful: it renders an explicit statement that the agent
	// has no tools, rather than leaving the model to assume it has some.
	Tools []ToolSummary
	// Channel names the delivery surface (e.g. "telegram") so the model can
	// size and format its reply for it.
	Channel string
	// Model is the active model reference, for the agent's self-report.
	Model string
	// SessionID identifies the conversation.
	SessionID string
	// Operator is the display name of the person this deployment assists,
	// from configuration. Empty renders nothing.
	//
	// It is a config value rather than prompt text because the operator's
	// name is deployment data, not part of Archie's identity: it was
	// previously compiled into the default persona, so every deployment
	// claimed to work for one particular person and that person's name was
	// sent to the model provider on every turn whether or not it was theirs.
	Operator string
	// Page is the dashboard route the operator is on right now (e.g.
	// "/tasks"). It is supplied per message by the web channel; empty for
	// non-web channels, which have no page context. Rendered in the <env>
	// runtime-metadata block so the agent knows where the operator is
	// looking and can point them somewhere relevant.
	Page string
	// Now stamps the prompt's date. The zero value omits nothing -- it
	// simply renders the zero date -- so callers should always set it.
	Now time.Time
}

// promptData is the template execution context. It exists so the template
// sees a pre-formatted date and pre-flattened tool metadata rather than
// calling methods during rendering.
type promptData struct {
	SystemPromptConfig
	Date string
}

// flattenWhitespace collapses every run of whitespace into a single space.
//
// The <tools> block renders one line per tool. A description containing a
// newline would otherwise split across lines, and a line beginning "- " would
// read as another tool entirely  --  letting a third-party MCP server
// advertise capabilities the agent does not have. Markup escaping does not
// help here, because the forged entry needs no markup.
func flattenWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

var archiePromptTemplate = template.Must(
	template.New("archie").
		Funcs(template.FuncMap{"xml": escapeXML}).
		Parse(archiePromptTpl),
)

// markupEscaper neutralises the three characters that can forge prompt
// structure, and nothing else.
//
// encoding/xml's EscapeText would be the obvious choice, but it also rewrites
// newlines, tabs and apostrophes into numeric entities  --  so a multi-line
// tool description arrives at the model as &#xA; noise. The prompt is read by
// a model, not parsed by an XML parser, so readability wins everywhere the
// security property is not at stake.
var markupEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

// escapeXML escapes markup in untrusted text. Tool names and descriptions
// arrive from MCP servers, which are third-party data: without escaping, a
// description containing a closing tag could forge prompt structure and
// appear to the model as trusted instructions.
func escapeXML(value string) string {
	return markupEscaper.Replace(value)
}

// BuildSystemPrompt renders the chat agent's system prompt.
//
// Rendering cannot fail at runtime: the template is parsed at init, and a
// template that somehow errors mid-execution would leave the agent with a
// truncated prompt, so the partial output is discarded in favour of the
// invariant rules.
func BuildSystemPrompt(cfg SystemPromptConfig) string {
	data := promptData{
		SystemPromptConfig: cfg,
		Date:               cfg.Now.Format("2006-01-02"),
	}
	data.Tools = make([]ToolSummary, len(cfg.Tools))
	for i, tool := range cfg.Tools {
		data.Tools[i] = ToolSummary{
			Name:        flattenWhitespace(tool.Name),
			Description: flattenWhitespace(tool.Description),
		}
	}
	var buf strings.Builder
	if err := archiePromptTemplate.Execute(&buf, data); err != nil {
		return fallbackSystemPrompt
	}
	return strings.TrimSpace(buf.String())
}

// fallbackSystemPrompt is the last-resort prompt used if template execution
// fails. It keeps the two rules whose absence caused real misbehaviour:
// never claim unverified capabilities, and stay brief.
const fallbackSystemPrompt = "You are Archie, a coding and project assistant. " +
	"Never claim a tool, file, memory, action or result you have not verified. " +
	"Keep replies short and lead with the answer."
