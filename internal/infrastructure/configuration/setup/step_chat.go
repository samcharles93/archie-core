package setup

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/tomlwrite"
)

// telegramTokenEnv is the env var a prompted Telegram bot token is stored
// under.
const telegramTokenEnv = "ARCHIE_TELEGRAM_TOKEN"

func stepChat(ctx context.Context, p Prompter, secrets SecretSink, params Params) (tableEdits, error) {
	wantTelegram := len(params.TelegramUserIDs) > 0
	if !wantTelegram {
		var err error
		wantTelegram, err = p.Confirm(ctx, "Configure a Telegram chat channel?", false)
		if err != nil {
			return nil, fmt.Errorf("setup: telegram: %w", err)
		}
	}
	if !wantTelegram {
		return nil, nil
	}

	// A reference means the value already lives in a secret engine: write the
	// reference and ask nothing. Otherwise the token comes from a prompt, and
	// only from a prompt; see stepForgeWithToken.
	tokenValue := tomlwrite.Ref("env", telegramTokenEnv)
	if ref := params.TelegramTokenRef; ref != (config.SecretRef{}) {
		tokenValue = tomlwrite.Ref(ref.Engine, ref.Key)
	} else {
		token, err := p.ReadSecret(ctx, "Telegram bot token (from @BotFather): ")
		if err != nil {
			return nil, fmt.Errorf("setup: telegram token: %w", err)
		}
		if strings.TrimSpace(token) == "" {
			return nil, nil
		}
		if err := secrets.Put("env", telegramTokenEnv, token); err != nil {
			return nil, fmt.Errorf("setup: store telegram token: %w", err)
		}
	}

	// TelegramConfig.AllowedUserIDs is deny-by-default: a bot token with no
	// allowlist answers nobody, which reads as broken rather than as the
	// safe default it actually is. Require at least one ID rather than
	// shipping a channel that silently does nothing.
	ids := params.TelegramUserIDs
	if len(ids) == 0 {
		idsLine, err := p.ReadLine(ctx, "Allowed Telegram user IDs (comma-separated; required -- the bot answers nobody without this): ", "")
		if err != nil {
			return nil, fmt.Errorf("setup: telegram allowed user ids: %w", err)
		}
		ids, err = ParseTelegramUserIDs(idsLine)
		if err != nil {
			return nil, fmt.Errorf("setup: telegram allowed user ids: %w", err)
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("setup: at least one allowed Telegram user ID is required, or the bot will answer nobody")
		}
	}

	return tableEdits{
		"chat.telegram": {
			"token":            tokenValue,
			"allowed_user_ids": intArrayLiteral(ids),
		},
	}, nil
}

// ParseTelegramUserIDs parses a comma-separated Telegram allowlist into
// int64 IDs, skipping empty parts. It is exported so cmd/archied setup can
// parse -telegram-user-ids with exactly the same rules the interactive
// prompt uses, rather than a second copy that could drift.
func ParseTelegramUserIDs(line string) ([]int64, error) {
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
