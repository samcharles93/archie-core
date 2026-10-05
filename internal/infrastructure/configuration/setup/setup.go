// Package setup runs archied's first-run configuration flow and returns the
// TOML edits to persist it.
package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/tomlwrite"
)

// SecretSink receives the secret values setup collects. Put buffers; the
// caller commits once the generated config is proven loadable.
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
	BotUser     string
	Operator    string
	ForgeHost   string
	DatabaseURL string
}

// tableEdits groups pending edits by table before Run flattens them into
// []tomlwrite.Edit, so a step can build "everything for [forge]" as one
// value and let Run decide the flattening order.
type tableEdits = map[string]map[string]string

// Params pre-answers setup questions; a step prompts only for unset fields.
// Provider is a config class; Model is the bare model name.
type Params struct {
	DatabaseURL string
	BotUser     string
	Operator    string
	ForgeType   string // github | gitea | none
	ForgeHost   string
	Provider    string // openai | anthropic | openrouter | gemini | groq | deepseek | mistral | ollama
	Model       string // bare model name; the step adds the "class/" prefix

	// TelegramUserIDs non-empty configures Telegram. The reference fields name
	// already-stored secrets as engine:key; when set, the step writes the
	// reference without prompting.
	ForgeTokenRef     config.SecretRef
	ProviderAPIKeyRef config.SecretRef
	TelegramTokenRef  config.SecretRef

	TelegramUserIDs []int64

	// Secret values are never parameters; they come from a prompt through
	// SecretSink.
}

// Run drives the setup flow and returns the TOML edits to apply. It writes no
// files; the caller must load the result before installing it or committing
// secrets.
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

	dbURL, err := stepDatabase(ctx, p, secrets, existing.DatabaseURL, params.DatabaseURL)
	if err != nil {
		return nil, err
	}
	add(tableEdits{"": {"database_url": tomlwrite.String(dbURL)}})

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

// stepDatabase returns the PostgreSQL URL. A blank answer on a fresh install
// means the bundled Compose database with a generated password. The password
// goes only to the env file as PGPASSWORD, which pgx and Compose both read, so
// config.toml holds no secret.
func stepDatabase(ctx context.Context, p Prompter, secrets SecretSink, existing, param string) (string, error) {
	if strings.TrimSpace(param) != "" {
		return param, nil
	}
	prompt := "PostgreSQL 18 URL (blank for the bundled database with a generated password): "
	if existing != "" {
		prompt = "PostgreSQL 18 URL: "
	}
	url, err := p.ReadLine(ctx, prompt, existing)
	if err != nil {
		return "", fmt.Errorf("setup: database url: %w", err)
	}
	if strings.TrimSpace(url) != "" {
		return url, nil
	}
	pw := make([]byte, 24)
	if _, err := rand.Read(pw); err != nil {
		return "", fmt.Errorf("setup: generate database password: %w", err)
	}
	password := hex.EncodeToString(pw)
	if err := secrets.Put("env", "PGPASSWORD", password); err != nil {
		return "", fmt.Errorf("setup: store database password: %w", err)
	}
	return "postgres://archie@127.0.0.1:5432/archie?sslmode=disable", nil
}
