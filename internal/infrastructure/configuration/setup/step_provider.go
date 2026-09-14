package setup

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
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
// requires the bws CLI on PATH; on a machine without it (the common case)
// resolving that key fails, and resolveProviderSecrets disables the provider
// with a warning rather than stopping the daemon. Neutralising the table is
// still setup's business: a provider the operator never chose should not be
// left advertised and unusable, with a warning they did not cause. Choosing any
// provider other than OpenAI, or choosing OpenAI but leaving its key blank,
// therefore replaces this table.
const templateDefaultActiveProvider = "openai"

func stepProvider(ctx context.Context, p Prompter, discovery ModelDiscovery, secrets SecretSink, params Params) (tableEdits, string, error) {
	options := make([]string, 0, len(cloudProviders)+1)
	for _, cp := range cloudProviders {
		options = append(options, cp.name)
	}
	options = append(options, "Self-hosted (Ollama)")

	var choice int
	if strings.TrimSpace(params.Provider) != "" {
		var err error
		choice, err = providerChoice(params.Provider)
		if err != nil {
			return nil, "", fmt.Errorf("setup: provider: %w", err)
		}
	} else {
		var err error
		choice, err = p.Select(ctx, "LLM provider:", options)
		if err != nil {
			return nil, "", fmt.Errorf("setup: provider: %w", err)
		}
	}

	var edits tableEdits
	var model string
	var err error
	if choice == len(cloudProviders) {
		if params.ProviderAPIKeyRef != (config.SecretRef{}) {
			return nil, "", fmt.Errorf("setup: a provider key reference was given, but the self-hosted provider is keyless, so nothing would use it")
		}
		edits, model, err = stepSelfHostedModel(ctx, p, discovery, params.Model)
	} else {
		edits, model, err = stepCloudProvider(ctx, p, secrets, cloudProviders[choice], params.Model, params.ProviderAPIKeyRef)
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

// providerChoice maps a typed Params.Provider class to the same select index
// the interactive prompt uses, so a supplied provider and a prompted one land
// in exactly the same step logic.
func providerChoice(name string) (int, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "ollama" {
		return len(cloudProviders), nil
	}
	for i, cp := range cloudProviders {
		if cp.class == name {
			return i, nil
		}
	}
	return -1, fmt.Errorf("unknown provider %q", name)
}

func stepSelfHostedModel(ctx context.Context, p Prompter, discovery ModelDiscovery, modelParam string) (tableEdits, string, error) {
	var model string
	if strings.TrimSpace(modelParam) != "" {
		model = modelParam
	} else if discovery != nil {
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

func stepCloudProvider(ctx context.Context, p Prompter, secrets SecretSink, cp cloudProvider, modelParam string, keyRef config.SecretRef) (tableEdits, string, error) {
	table := "providers." + cp.class
	edits := tableEdits{table: {"class": tomlwrite.String(cp.class)}}

	if keyRef != (config.SecretRef{}) {
		// A reference means the value already lives in a secret engine: write
		// the reference and ask nothing. Setup neither reads nor stores it,
		// because it does not own it.
		edits[table]["api_key"] = tomlwrite.Ref(keyRef.Engine, keyRef.Key)
	} else {
		keyEdit, err := cloudKeyEdit(ctx, p, secrets, cp)
		if err != nil {
			return nil, "", err
		}
		if keyEdit != "" {
			edits[table]["api_key"] = keyEdit
		}
	}

	model, err := cloudModel(ctx, p, cp, modelParam)
	if err != nil {
		return nil, "", err
	}
	return edits, cp.class + "/" + model, nil
}

// cloudKeyEdit obtains the api_key edit by prompting. An empty return means
// "write no api_key and leave whatever the template has", which is the
// configure-later case.
//
// There is no key parameter. A value supplied as one would arrive from a
// command line and bypass the secret engine entirely; see Params.
func cloudKeyEdit(ctx context.Context, p Prompter, secrets SecretSink, cp cloudProvider) (string, error) {
	key, err := p.ReadSecret(ctx, fmt.Sprintf("%s API key: ", cp.name))
	if err != nil {
		return "", fmt.Errorf("setup: %s api key: %w", cp.name, err)
	}
	if strings.TrimSpace(key) == "" {
		if cp.class == templateDefaultActiveProvider {
			// Skipping the key normally means "configure later" and leaves
			// api_key untouched -- but for openai specifically that would leave
			// the template's own unresolvable bws default active. See
			// templateDefaultActiveProvider.
			return tomlwrite.Ref("", ""), nil
		}
		return "", nil
	}
	if err := secrets.Put("env", cp.apiKeyEnv, key); err != nil {
		return "", fmt.Errorf("setup: store %s api key: %w", cp.name, err)
	}
	return tomlwrite.Ref("env", cp.apiKeyEnv), nil
}

// cloudModel resolves the model name: the parameter when given, otherwise a
// prompt, and an error when neither yields one.
func cloudModel(ctx context.Context, p Prompter, cp cloudProvider, modelParam string) (string, error) {
	model := modelParam
	if strings.TrimSpace(model) == "" {
		var err error
		model, err = p.ReadLine(ctx, fmt.Sprintf("Model for %s (e.g. gpt-5.4): ", cp.name), "")
		if err != nil {
			return "", fmt.Errorf("setup: model name: %w", err)
		}
	}
	if strings.TrimSpace(model) == "" {
		return "", fmt.Errorf("setup: a model name is required for %s", cp.name)
	}
	return model, nil
}
