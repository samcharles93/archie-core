package setup

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/tomlwrite"
)

// cloudProvider is one selectable cloud LLM provider: the config class it
// writes and the env var name its secret is stored under. Both the
// provider step (which writes providers.<name>.api_key = {engine, key})
// and the secret sink (which is asked to store the value under exactly
// that key) read from this single table, so the two can never name the
// secret differently -- the exact class of bug archie-core-cbk exists to
// close for forge tokens, applied the same way to provider keys.
type cloudProvider struct {
	name      string
	class     string
	apiKeyEnv string
}

var cloudProviders = []cloudProvider{
	{name: "OpenAI", class: "openai", apiKeyEnv: "OPENAI_API_KEY"},
	{name: "Anthropic", class: "anthropic", apiKeyEnv: "ANTHROPIC_API_KEY"},
	{name: "OpenRouter", class: "openrouter", apiKeyEnv: "OPENROUTER_API_KEY"},
	{name: "Gemini", class: "gemini", apiKeyEnv: "GEMINI_API_KEY"},
	{name: "Groq", class: "groq", apiKeyEnv: "GROQ_API_KEY"},
	{name: "DeepSeek", class: "deepseek", apiKeyEnv: "DEEPSEEK_API_KEY"},
	{name: "Mistral", class: "mistral", apiKeyEnv: "MISTRAL_API_KEY"},
}

// templateDefaultActiveProvider is the one provider table config.example.
// toml ships active by default: [providers.openai], with
// api_key = {engine="bws", key="OPENAI_API_KEY"}. bws is compiled in but
// requires the bws CLI on PATH; on a machine without it (the common case),
// resolving that key errors, and cmd/archied/provider_secrets.go's
// resolveProviderSecrets walks every entry in cfg.Providers -- not just
// the ones [models] actually references -- and refuses to start the
// daemon at all if any of them fails to resolve. Choosing any provider
// other than OpenAI, or choosing OpenAI but leaving its key blank, must
// neutralise this table rather than leave it as a silent boot-time
// landmine nothing in setup's own output would explain.
const templateDefaultActiveProvider = "openai"

func stepProvider(ctx context.Context, p Prompter, discovery ModelDiscovery, secrets SecretSink) (tableEdits, string, error) {
	options := make([]string, 0, len(cloudProviders)+1)
	for _, cp := range cloudProviders {
		options = append(options, cp.name)
	}
	options = append(options, "Self-hosted (Ollama)")

	choice, err := p.Select(ctx, "LLM provider:", options)
	if err != nil {
		return nil, "", fmt.Errorf("setup: provider: %w", err)
	}

	var edits tableEdits
	var model string
	if choice == len(cloudProviders) {
		edits, model, err = stepSelfHostedModel(ctx, p, discovery)
	} else {
		edits, model, err = stepCloudProvider(ctx, p, secrets, cloudProviders[choice])
	}
	if err != nil {
		return nil, "", err
	}

	openaiTable := "providers." + templateDefaultActiveProvider
	if _, chosenOpenAI := edits[openaiTable]; !chosenOpenAI {
		edits[openaiTable] = map[string]string{"api_key": tomlwrite.Ref("", "")}
	}
	return edits, model, nil
}

func stepSelfHostedModel(ctx context.Context, p Prompter, discovery ModelDiscovery) (tableEdits, string, error) {
	var model string
	if discovery != nil {
		if models, err := discovery.ListOllamaModels(ctx); err == nil && len(models) > 0 {
			idx, err := p.Select(ctx, "Ollama model:", models)
			if err != nil {
				return nil, "", fmt.Errorf("setup: model selection: %w", err)
			}
			model = models[idx]
		}
	}
	if model == "" {
		var err error
		model, err = p.ReadLine(ctx, "Ollama model name (e.g. llama3): ", "llama3")
		if err != nil {
			return nil, "", fmt.Errorf("setup: model name: %w", err)
		}
	}
	edits := tableEdits{"providers.ollama": {"class": tomlwrite.String("ollama")}}
	return edits, "ollama/" + model, nil
}

func stepCloudProvider(ctx context.Context, p Prompter, secrets SecretSink, cp cloudProvider) (tableEdits, string, error) {
	key, err := p.ReadSecret(ctx, fmt.Sprintf("%s API key: ", cp.name))
	if err != nil {
		return nil, "", fmt.Errorf("setup: %s api key: %w", cp.name, err)
	}
	table := "providers." + cp.class
	edits := tableEdits{table: {"class": tomlwrite.String(cp.class)}}
	if strings.TrimSpace(key) != "" {
		if err := secrets.Put("env", cp.apiKeyEnv, key); err != nil {
			return nil, "", fmt.Errorf("setup: store %s api key: %w", cp.name, err)
		}
		edits[table]["api_key"] = tomlwrite.Ref("env", cp.apiKeyEnv)
	} else if cp.class == templateDefaultActiveProvider {
		// Skipping the key normally means "configure later" and leaves
		// api_key untouched -- but for openai specifically that would
		// leave the template's own unresolvable bws default active. See
		// templateDefaultActiveProvider.
		edits[table]["api_key"] = tomlwrite.Ref("", "")
	}

	model, err := p.ReadLine(ctx, fmt.Sprintf("Model for %s (e.g. gpt-5.4): ", cp.name), "")
	if err != nil {
		return nil, "", fmt.Errorf("setup: model name: %w", err)
	}
	if strings.TrimSpace(model) == "" {
		return nil, "", fmt.Errorf("setup: a model name is required for %s", cp.name)
	}
	return edits, cp.class + "/" + model, nil
}
