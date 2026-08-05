package setup

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/tomlwrite"
)

// telegramTokenEnv is the env var Telegram's bot token is stored under.
// TelegramConfig has no secret.SecretRef field -- unlike the forge and
// provider steps, the config edit is a plain token_env string, not a
// {engine,key} inline table.
const telegramTokenEnv = "ARCHIE_TELEGRAM_TOKEN"

func stepChat(ctx context.Context, p Prompter, secrets SecretSink) (tableEdits, error) {
	wantTelegram, err := p.Confirm(ctx, "Configure a Telegram chat channel?", false)
	if err != nil {
		return nil, fmt.Errorf("setup: telegram: %w", err)
	}
	if !wantTelegram {
		return nil, nil
	}

	token, err := p.ReadSecret(ctx, "Telegram bot token (from @BotFather): ")
	if err != nil {
		return nil, fmt.Errorf("setup: telegram token: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return nil, nil
	}

	// TelegramConfig.AllowedUserIDs is deny-by-default: a bot token with no
	// allowlist answers nobody, which reads as broken rather than as the
	// safe default it actually is. Require at least one ID rather than
	// shipping a channel that silently does nothing.
	idsLine, err := p.ReadLine(ctx, "Allowed Telegram user IDs (comma-separated; required -- the bot answers nobody without this): ", "")
	if err != nil {
		return nil, fmt.Errorf("setup: telegram allowed user ids: %w", err)
	}
	ids, err := parseUserIDs(idsLine)
	if err != nil {
		return nil, fmt.Errorf("setup: telegram allowed user ids: %w", err)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("setup: at least one allowed Telegram user ID is required, or the bot will answer nobody")
	}

	if err := secrets.Put("env", telegramTokenEnv, token); err != nil {
		return nil, fmt.Errorf("setup: store telegram token: %w", err)
	}

	return tableEdits{
		"chat.telegram": {
			"token_env":        tomlwrite.String(telegramTokenEnv),
			"allowed_user_ids": intArrayLiteral(ids),
		},
	}, nil
}

func parseUserIDs(line string) ([]int64, error) {
	var ids []int64
	for part := range strings.SplitSeq(line, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a valid Telegram user ID: %w", part, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// intArrayLiteral renders a single-line TOML integer array. tomlwrite only
// understands single-line values; a Telegram allowlist is short enough
// that this never needs to wrap.
func intArrayLiteral(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
