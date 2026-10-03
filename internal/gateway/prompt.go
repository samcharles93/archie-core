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

// RepoEnv describes one managed repository for the chat prompt's <env> block.
type RepoEnv struct {
	// FullName is the configured owner/name.
	FullName string
	// Forge is the forge host the repository lives on (forge.host). Empty
	// means no forge is configured for the active identity.
	Forge string
	// DefaultBranch is the branch PRs target (repos[].base, "main" when
	// unset) -- the branch the daemon actually uses, not a guess.
	DefaultBranch string
}

// SystemPromptConfig holds the inputs for one rendered system prompt.
type SystemPromptConfig struct {
	// Persona is the active persona prompt.
	Persona string
	// Tools is the complete set of tools available for this turn.
	Tools []ToolSummary
	// Channel names the communication channel.
	Channel   string
	Model     string
	SessionID string
	// Page is the dashboard route the operator is on; empty outside the web
	// channel.
	Page string
	// Now stamps the prompt's date.
	Now time.Time
	// Workspace is the directory the chat agent's file and shell tools are
	// rooted at (chat.workspace). Empty means the agent has no rooted
	// workspace: a real fact the prompt must say explicitly ("unknown") so the
	// agent does not guess a path the daemon never granted it.
	Workspace string
	// Repos lists the repositories under management with their forge host and
	// default branch. Rendered from live configuration (repos entries) so the
	// agent knows its scope without probing the filesystem.
	Repos []RepoEnv
	// Operator is the display name of the person this deployment assists
	// (chat.operator). Rendered so the agent knows the identity context it
	// serves under; empty means unconfigured and must be said explicitly.
	Operator string
	// Memory is the rendered <memory> block body; empty omits the block.
	Memory string
}

// promptData is the template execution context. It exists so the template
// sees a pre-formatted date and pre-flattened tool metadata rather than
// calling methods during rendering.
type promptData struct {
	SystemPromptConfig
	Date string
}

// escapeXML replaces the three bytes that would otherwise be read as markup
// inside a trust="data" block. Exported within the package so callers that
// must bound a block's *rendered* size (turn_memory.go's byte cap) measure
// the same expansion the template applies, rather than the pre-escape size.
var escapeXML = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
).Replace

var archiePromptTemplate = template.Must(
	template.New("archie").
		Funcs(template.FuncMap{"xml": escapeXML}).
		Parse(archiePromptTpl),
)

// BuildSystemPrompt renders the chat agent's system prompt.
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
		Date:      cfg.Now.Format("2006-01-02"),
		Workspace: cfg.Workspace,
		Repos:     cfg.Repos,
		Operator:  cfg.Operator,
		Memory:    cfg.Memory,
	}
	var buf strings.Builder
	if err := archiePromptTemplate.Execute(&buf, data); err != nil {
		tpl := "You are Archie, a coding and project assistant. " +
			"Never claim a tool, file, memory, action or result you have not verified. " +
			"Keep replies short and lead with the answer."
		return tpl
	}
	return strings.TrimSpace(buf.String())
}
