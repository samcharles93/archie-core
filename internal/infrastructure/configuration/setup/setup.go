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

// SecretSink is where setup sends the secret values a step collects. It is
// declared here, not implemented: setup edits config.toml keys, it does not
// write the env file itself. Put is expected to buffer rather than write
// immediately -- the caller commits only once the config text setup
// produced has been proven loadable, so a validation failure after some
// secrets were already prompted for never leaves a secret written without
// the config that references it, or vice versa.
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

// Run drives the interactive setup flow and returns the TOML edits to
// apply. It does not read or write any file itself: the caller renders the
// edits (tomlwrite.Generate against a fresh template, or tomlwrite.Apply
// against an existing config's bytes for a re-run) and must prove the
// result loads through a real configuration.Loader before installing it or
// calling secrets.Commit.
func Run(ctx context.Context, p Prompter, discovery ModelDiscovery, secrets SecretSink, existing ExistingValues) ([]tomlwrite.Edit, error) {
	var edits []tomlwrite.Edit
	add := func(all tableEdits) {
		for table, kv := range all {
			for k, v := range kv {
				edits = append(edits, tomlwrite.Edit{Table: table, Key: k, Value: v})
			}
		}
	}

	botUser, err := p.ReadLine(ctx, "Bot user (forge username for archied's commits and API calls): ", existing.BotUser)
	if err != nil {
		return nil, fmt.Errorf("setup: bot user: %w", err)
	}
	if strings.TrimSpace(botUser) == "" {
		return nil, fmt.Errorf("setup: bot user is required")
	}
	add(tableEdits{"": {"bot_user": tomlwrite.String(botUser)}})

	operator, err := p.ReadLine(ctx, "Operator display name (shown to the chat agent; blank to skip): ", existing.Operator)
	if err != nil {
		return nil, fmt.Errorf("setup: operator: %w", err)
	}
	if strings.TrimSpace(operator) != "" {
		add(tableEdits{"chat": {"operator": tomlwrite.String(operator)}})
	}

	providerEdits, model, err := stepProvider(ctx, p, discovery, secrets)
	if err != nil {
		return nil, err
	}
	add(providerEdits)
	for _, role := range []string{"triage", "planner", "builder"} {
		add(tableEdits{"models": {role: tomlwrite.String(model)}})
	}

	forgeEdits, err := stepForge(ctx, p, secrets, existing.ForgeHost)
	if err != nil {
		return nil, err
	}
	add(forgeEdits)

	chatEdits, err := stepChat(ctx, p, secrets)
	if err != nil {
		return nil, err
	}
	add(chatEdits)

	return edits, nil
}
