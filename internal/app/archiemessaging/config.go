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
	Email         config.EmailConfig
	Webhook       config.WebhookRoute
	WebhookAddr   string
}

type projection struct {
	gateway     ServiceTarget
	telegram    config.TelegramConfig
	email       config.EmailConfig
	webhook     config.WebhookRoute
	webhookAddr string
}

// Resolve loads the configuration, extracts the messaging projection, resolves
// credentials, and applies defaults.
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

	tgToken, err := resolveTelegramToken(proj.telegram)
	if err != nil {
		return ResolvedConfig{}, fmt.Errorf("resolve telegram token: %w", err)
	}

	return ResolvedConfig{
		Options:       o,
		TelegramToken: tgToken,
		Telegram:      proj.telegram,
		Email:         proj.email,
		Webhook:       proj.webhook,
	}, nil
}

func readProjection(path, overlay string, log *slog.Logger) (projection, bool, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
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
		telegram:    cfg.Chat.Telegram,
		email:       cfg.Chat.Email,
		webhook:     cfg.Chat.Webhook,
		webhookAddr: cfg.Chat.WebhookAddr,
	}
}

func withEnvTokens(o Options) Options {
	if o.Gateway.Token == "" {
		o.Gateway.Token = os.Getenv("GATEWAY_TOKEN")
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
	return o
}

func resolveTelegramToken(cfg config.TelegramConfig) (string, error) {
	if cfg.Token != (secret.SecretRef{}) {
		reg := secret.NewRegistry()
		return reg.Resolve(cfg.Token)
	}
	if cfg.TokenEnv != "" {
		return os.Getenv(cfg.TokenEnv), nil
	}
	return "", nil
}
