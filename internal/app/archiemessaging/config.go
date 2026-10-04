package archiemessaging

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/secret"
)

// ResolvedConfig contains the resolved inputs needed by the Messaging Service.
type ResolvedConfig struct {
	Options       Options
	TelegramToken string
	Telegram      config.TelegramConfig
	// WorkDir, BotUser and HealthURL are not channel transport settings. They
	// locate this identity's release-announcement and update-report state
	// files, and give the update installer the daemon health endpoint to poll
	// after it applies one.
	WorkDir       string
	BotUser       string
	HealthURL     string
	ShowToolCalls bool
}

type projection struct {
	gateway       ServiceTarget
	stateStore    ServiceTarget
	telegram      config.TelegramConfig
	workDir       string
	botUser       string
	healthURL     string
	showToolCalls bool
}

// Resolve loads the configuration, extracts the messaging projection and
// applies defaults. Credentials are not resolved here: they may name an
// extension engine that starts only after the State Store is dialed, so
// resolveTokens runs afterwards.
func Resolve(o Options, log *slog.Logger) (ResolvedConfig, error) {
	var proj projection
	if o.Config != "" {
		p, found, err := readProjection(o.Config, o.Overlay, log)
		if err != nil {
			return ResolvedConfig{}, err
		}
		if found {
			proj = p
			o = merge(o, proj)
		}
	}
	o = withDefaults(o)
	o = withEnvTokens(o)
	if err := o.validate(); err != nil {
		return ResolvedConfig{}, err
	}

	return ResolvedConfig{
		Options:       o,
		Telegram:      proj.telegram,
		WorkDir:       proj.workDir,
		BotUser:       proj.botUser,
		HealthURL:     proj.healthURL,
		ShowToolCalls: proj.showToolCalls,
	}, nil
}

func readProjection(path, overlay string, log *slog.Logger) (projection, bool, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) && overlay == "" {
		return projection{}, false, nil
	}
	doc, err := configuration.New(log).Resolve(path, overlay)
	if err != nil {
		return projection{}, false, fmt.Errorf("read messaging configuration: %w", err)
	}
	return project(doc.Config), true, nil
}

func project(cfg config.Config) projection {
	return projection{
		gateway: ServiceTarget{
			Target: cfg.Services.Get(config.ServiceNameGateway).Target,
			Token:  cfg.Services.Get(config.ServiceNameGateway).TargetToken,
		},
		stateStore:    ServiceTarget{Target: cfg.Services.Get(config.ServiceNameState).Target, Token: cfg.Services.Get(config.ServiceNameState).TargetToken},
		telegram:      cfg.Chat.Telegram,
		workDir:       cfg.WorkDir,
		botUser:       cfg.BotUser,
		healthURL:     cfg.Health.URL(),
		showToolCalls: cfg.Chat.ShowToolCalls,
	}
}

func withEnvTokens(o Options) Options {
	if o.Gateway.Token == "" {
		o.Gateway.Token = os.Getenv("GATEWAY_TOKEN")
	}
	if o.StateStore.Token == "" {
		o.StateStore.Token = os.Getenv("STATE_STORE_TOKEN")
	}
	return o
}

func merge(o Options, p projection) Options {
	if o.Gateway.Target == "" {
		o.Gateway.Target = p.gateway.Target
	}
	if o.Gateway.Token == "" {
		o.Gateway.Token = p.gateway.Token
	}
	if o.StateStore.Target == "" {
		o.StateStore.Target = p.stateStore.Target
	}
	if o.StateStore.Token == "" {
		o.StateStore.Token = p.stateStore.Token
	}
	return o
}

// resolveTelegramToken resolves the configured bot token; an unset
// reference means the channel is off.
func resolveTelegramToken(cfg config.TelegramConfig, secrets *secret.Registry) (string, error) {
	if cfg.Token == (secret.SecretRef{}) {
		return "", nil
	}
	return secrets.Resolve(cfg.Token)
}

// resolveTokens fills the channel credentials from the file configuration.
func (c *ResolvedConfig) resolveTokens(secrets *secret.Registry) error {
	token, err := resolveTelegramToken(c.Telegram, secrets)
	if err != nil {
		return fmt.Errorf("resolve telegram token: %w", err)
	}
	c.TelegramToken = token
	return nil
}
