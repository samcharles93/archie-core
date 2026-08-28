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
type SystemPromptConfig struct {
	// Persona is the active persona prompt.
	Persona string
	// Tools is the complete set of tools available for this turn.
	Tools []ToolSummary
	// Channel names the communication channel.
	Channel string
	Model string
	SessionID string
	// Page is the dashboard route the operator is on right now (e.g.
	// "/tasks"). It is supplied per message by the web channel; empty for
	// non-web channels, which have no page context. Rendered in the <env>
	// runtime-metadata block so the agent knows where the operator is
	// looking and can point them somewhere relevant.
	Page string
	// Now stamps the prompt's date.
	Now time.Time
}

// promptData is the template execution context. It exists so the template
// sees a pre-formatted date and pre-flattened tool metadata rather than
// calling methods during rendering.
type promptData struct {
	SystemPromptConfig
	Date string
}

var archiePromptTemplate = template.Must(
	template.New("archie").
		Funcs(template.FuncMap{"xml": strings.NewReplacer(
			"&", "&amp;",
			"<", "&lt;",
			">", "&gt;",
		)}).
		Parse(archiePromptTpl),
)

// BuildSystemPrompt renders the chat agent's system prompt.
//
// Rendering cannot fail at runtime: the template is parsed at init, and a
// template that somehow errors mid-execution would leave the agent with a
// truncated prompt, so the partial output is discarded in favour of the
// invariant rules.
func BuildSystemPrompt(cfg SystemPromptConfig) string {
	tools := make([]ToolSummary, len(cfg.Tools))
	for i, tool := range cfg.Tools {
		tools[i] = ToolSummary{
			Name:        strings.Join(strings.Fields(tool.Name), " "),
			Description: strings.Join(strings.Fields(tool.Description), " "),
		}
	}
	data := promptData{
			Persona:   cfg.Persona,
			Tools:     tools,
			Channel:   cfg.Channel,
			Model:     cfg.Model,
			SessionID: cfg.SessionID,
			Page:      cfg.Page,
			Now:       cfg.Now,
		Date: cfg.Now.Format("2006-01-02"),
	}
	var buf strings.Builder
	if err := archiePromptTemplate.Execute(&buf, data); err != nil {
		tpl :=  "You are Archie, a coding and project assistant. " +
				"Never claim a tool, file, memory, action or result you have not verified. " +
				"Keep replies short and lead with the answer."
		return tpl
	}
	return strings.TrimSpace(buf.String())
}
