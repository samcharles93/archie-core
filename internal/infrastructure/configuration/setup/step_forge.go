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

func stepForge(ctx context.Context, p Prompter, secrets SecretSink, existingHost string, params Params) (tableEdits, error) {
	var choice int
	if strings.TrimSpace(params.ForgeType) != "" {
		var err error
		choice, err = forgeChoice(params.ForgeType)
		if err != nil {
			return nil, fmt.Errorf("setup: forge: %w", err)
		}
	} else {
		var err error
		choice, err = p.Select(ctx, "Code forge:", []string{"GitHub", "Gitea", "None (standalone mode)"})
		if err != nil {
			return nil, fmt.Errorf("setup: forge: %w", err)
		}
	}

	switch choice {
	case 0:
		host := params.ForgeHost
		if strings.TrimSpace(host) == "" {
			host = "https://github.com"
		}
		return stepForgeWithToken(ctx, p, secrets, "github", host, "GitHub token (leave blank to configure later): ", params.ForgeToken)
	case 1:
		defaultHost := existingHost
		if defaultHost == "" {
			defaultHost = "https://gitea.example.com"
		}
		host := params.ForgeHost
		if strings.TrimSpace(host) == "" {
			var err error
			host, err = p.ReadLine(ctx, "Gitea base URL: ", defaultHost)
			if err != nil {
				return nil, fmt.Errorf("setup: gitea host: %w", err)
			}
		}
		return stepForgeWithToken(ctx, p, secrets, "gitea", host, "Gitea token (leave blank to configure later): ", params.ForgeToken)
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

// forgeChoice maps a typed Params.ForgeType to the same select index the
// interactive prompt uses, so a supplied forge type and a prompted one land
// in exactly the same step logic.
func forgeChoice(name string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "github":
		return 0, nil
	case "gitea":
		return 1, nil
	case "none":
		return 2, nil
	default:
		return -1, fmt.Errorf("unknown forge type %q (want \"github\", \"gitea\", or \"none\")", name)
	}
}

func stepForgeWithToken(ctx context.Context, p Prompter, secrets SecretSink, forgeType, host, tokenPrompt, tokenParam string) (tableEdits, error) {
	edits := tableEdits{"forge": {
		"type": tomlwrite.String(forgeType),
		"host": tomlwrite.String(host),
	}}
	token := tokenParam
	if token == "" {
		var err error
		token, err = p.ReadSecret(ctx, tokenPrompt)
		if err != nil {
			return nil, fmt.Errorf("setup: %s token: %w", forgeType, err)
		}
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
