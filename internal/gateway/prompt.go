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

// RepoEnv is the runtime-metadata summary of one repository under
// management, rendered into the chat prompt's <env> block so the agent knows
// which checkouts it is responsible for without probing the filesystem. It is
// built only from live configuration; it never names a repository the daemon
// cannot confirm.
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
	// Page is the dashboard route the operator is on right now (e.g.
	// "/tasks"). It is supplied per message by the web channel; empty for
	// non-web channels, which have no page context. Rendered in the <env>
	// runtime-metadata block so the agent knows where the operator is
	// looking and can point them somewhere relevant.
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
		).Replace}).
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
