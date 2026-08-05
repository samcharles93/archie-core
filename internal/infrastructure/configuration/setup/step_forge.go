package setup

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/tomlwrite"
)

// forgeToken names the env var a forge type's token is stored under. Used
// by both the config edit (forge.token = {engine="env", key=...}) and the
// secret sink, for the same reason cloudProviders is a single table in
// step_provider.go: the installer's original bug was these two writes
// disagreeing.
var forgeToken = map[string]string{
	"github": "ARCHIE_GITHUB_TOKEN",
	"gitea":  "ARCHIE_GITEA_TOKEN",
}

func stepForge(ctx context.Context, p Prompter, secrets SecretSink, existingHost string) (tableEdits, error) {
	choice, err := p.Select(ctx, "Code forge:", []string{"GitHub", "Gitea", "None (standalone mode)"})
	if err != nil {
		return nil, fmt.Errorf("setup: forge: %w", err)
	}

	switch choice {
	case 0:
		return stepForgeWithToken(ctx, p, secrets, "github", "https://github.com", "GitHub token (leave blank to configure later): ")
	case 1:
		defaultHost := existingHost
		if defaultHost == "" {
			defaultHost = "https://gitea.example.com"
		}
		host, err := p.ReadLine(ctx, "Gitea base URL: ", defaultHost)
		if err != nil {
			return nil, fmt.Errorf("setup: gitea host: %w", err)
		}
		return stepForgeWithToken(ctx, p, secrets, "gitea", host, "Gitea token (leave blank to configure later): ")
	default:
		// The template's default [forge] block is active (type = "github"
		// with a host and token already set). Selecting "none" must
		// overwrite host and token too, not just type -- otherwise a
		// disabled forge still shows a token reference nobody configured
		// and that resolveForge never reads, which is exactly the kind of
		// stale/misleading value this feature exists to stop shipping.
		return tableEdits{"forge": {
			"type":  tomlwrite.String("none"),
			"host":  tomlwrite.String(""),
			"token": tomlwrite.Ref("", ""),
		}}, nil
	}
}

func stepForgeWithToken(ctx context.Context, p Prompter, secrets SecretSink, forgeType, host, tokenPrompt string) (tableEdits, error) {
	edits := tableEdits{"forge": {
		"type": tomlwrite.String(forgeType),
		"host": tomlwrite.String(host),
	}}
	token, err := p.ReadSecret(ctx, tokenPrompt)
	if err != nil {
		return nil, fmt.Errorf("setup: %s token: %w", forgeType, err)
	}
	if strings.TrimSpace(token) == "" {
		return edits, nil
	}
	envKey := forgeToken[forgeType]
	if err := secrets.Put("env", envKey, token); err != nil {
		return nil, fmt.Errorf("setup: store %s token: %w", forgeType, err)
	}
	edits["forge"]["token"] = tomlwrite.Ref("env", envKey)
	return edits, nil
}
