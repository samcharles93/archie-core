// Package setup drives archied's interactive first-run configuration flow
// and produces the TOML edits to persist it. See the [Run] doc comment for
// why it returns edits rather than a config.Config: the whole point of this
// feature is to stop the config-writing code and the config-reading code
// disagreeing about the schema, and building a second, parallel
// representation to validate against would reintroduce exactly that risk
// inside setup itself.
package setup

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/tomlwrite"
)

// SecretSink is where setup sends the secret values a step collects. setup
// edits config.toml keys; it does not write the env file itself. Put is
// expected to buffer rather than write immediately -- the caller commits only
// once the config text setup produced has been proven loadable, so a
// validation failure after some secrets were already prompted for never
// leaves a secret written without the config that references it, or vice
// versa. [EnvFileSink] is the concrete implementation for the env file that
// sits beside config.toml; the caller owns the path and constructs it.
type SecretSink interface {
	// Put records that key (as looked up through engine) must resolve to
	// value once Commit is called.
	Put(engine, key, value string) error
	// Commit persists everything Put has recorded so far.
	Commit() error
}

// ModelDiscovery finds locally available models for the self-hosted
// provider path. A nil ModelDiscovery, or one that returns an error,
// degrades to a free-text model name -- discovery is a convenience, not a
// requirement.
type ModelDiscovery interface {
	ListOllamaModels(ctx context.Context) ([]string, error)
}

// ExistingValues prefills prompts on a re-run. The zero value is correct
// for a fresh install.
type ExistingValues struct {
	BotUser   string
	Operator  string
	ForgeHost string
}

// tableEdits groups pending edits by table before Run flattens them into
// []tomlwrite.Edit, so a step can build "everything for [forge]" as one
// value and let Run decide the flattening order.
type tableEdits = map[string]map[string]string

// Params supplies pre-answered values for the setup flow's question sites.
// Each step consults Params first and only asks the Prompter when the
// corresponding field is unset, so an unattended install can state what it
// wants without a terminal and without matching against prompt text -- the
// brittleness class removed in 9a4f11c (prompt text is not a stable key for
// a flag-to-answer mapping).
//
// Provider is a config class ("openai", "anthropic", "openrouter", "gemini",
// "groq", "deepseek", "mistral", or "ollama" for the self-hosted path). Model
// is the bare model name; the step adds the "class/" prefix. TelegramUserIDs
// non-empty is the signal to configure Telegram -- the step already refuses
// an empty allowlist, so an empty slice means "ask" (or "skip" when the
// prompter's default is no).
type Params struct {
	BotUser   string
	Operator  string
	ForgeType string // github | gitea | none
	ForgeHost string
	Provider  string // openai | anthropic | openrouter | gemini | groq | deepseek | mistral | ollama
	Model     string // bare model name; the step adds the "class/" prefix

	// TelegramUserIDs is an access policy, not a credential: IDs are not secret.
	// Non-empty means "configure Telegram".
	TelegramUserIDs []int64

	// There is deliberately no secret-valued field here, for forge tokens,
	// provider API keys or bot tokens alike. Secrets are set in a secret engine
	// and this flow only ever writes a reference to one; a value in Params would
	// arrive from a command line, landing in shell history and process listings,
	// and would be a second way to set a secret that no engine knows about.
	// Values reach an engine through SecretSink, from a prompt.
}

// Run drives the interactive setup flow and returns the TOML edits to
// apply. It does not read or write any file itself: the caller renders the
// edits (tomlwrite.Generate against a fresh template, or tomlwrite.Apply
// against an existing config's bytes for a re-run) and must prove the
// result loads through a real configuration.Loader before installing it or
// calling secrets.Commit.
func Run(ctx context.Context, p Prompter, discovery ModelDiscovery, secrets SecretSink, existing ExistingValues) ([]tomlwrite.Edit, error) {
	return RunParams(ctx, p, discovery, secrets, existing, Params{})
}

// RunParams is Run with the question sites pre-answered from params. A zero
// Params behaves exactly like Run.
func RunParams(ctx context.Context, p Prompter, discovery ModelDiscovery, secrets SecretSink, existing ExistingValues, params Params) ([]tomlwrite.Edit, error) {
	var edits []tomlwrite.Edit
	add := func(all tableEdits) {
		for table, kv := range all {
			for k, v := range kv {
				edits = append(edits, tomlwrite.Edit{Table: table, Key: k, Value: v})
			}
		}
	}

	botUser := params.BotUser
	if strings.TrimSpace(botUser) == "" {
		var err error
		botUser, err = p.ReadLine(ctx, "Bot user (forge username for archied's commits and API calls): ", existing.BotUser)
		if err != nil {
			return nil, fmt.Errorf("setup: bot user: %w", err)
		}
	}
	if strings.TrimSpace(botUser) == "" {
		return nil, fmt.Errorf("setup: bot user is required")
	}
	add(tableEdits{"": {"bot_user": tomlwrite.String(botUser)}})

	operator := params.Operator
	if strings.TrimSpace(operator) == "" {
		var err error
		operator, err = p.ReadLine(ctx, "Operator display name (shown to the chat agent; blank to skip): ", existing.Operator)
		if err != nil {
			return nil, fmt.Errorf("setup: operator: %w", err)
		}
	}
	if strings.TrimSpace(operator) != "" {
		add(tableEdits{"chat": {"operator": tomlwrite.String(operator)}})
	}

	providerEdits, model, err := stepProvider(ctx, p, discovery, secrets, params)
	if err != nil {
		return nil, err
	}
	add(providerEdits)
	for _, role := range []string{"triage", "planner", "builder"} {
		add(tableEdits{"models": {role: tomlwrite.String(model)}})
	}

	forgeEdits, err := stepForge(ctx, p, secrets, existing.ForgeHost, params)
	if err != nil {
		return nil, err
	}
	add(forgeEdits)

	chatEdits, err := stepChat(ctx, p, secrets, params)
	if err != nil {
		return nil, err
	}
	add(chatEdits)

	return edits, nil
}
