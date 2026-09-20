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
	WebhookSecret string
	WebhookAddr   string
	// WorkDir, BotUser and HealthURL are not channel transport settings. They
	// locate this identity's release-announcement and update-report state
	// files, and give the update installer the daemon health endpoint to poll
	// after it applies one (docs/architecture/migration-decisions.md, "Telegram
	// operator surface after extraction").
	WorkDir       string
	BotUser       string
	HealthURL     string
	ShowToolCalls bool
}

type projection struct {
	gateway       ServiceTarget
	stateStore    ServiceTarget
	telegram      config.TelegramConfig
	email         config.EmailConfig
	webhook       config.WebhookRoute
	webhookAddr   string
	workDir       string
	botUser       string
	healthURL     string
	showToolCalls bool
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

	secrets := secret.NewRegistry()
	tgToken, err := resolveTelegramToken(proj.telegram, secrets)
	if err != nil {
		return ResolvedConfig{}, fmt.Errorf("resolve telegram token: %w", err)
	}

	// An unresolvable webhook secret is not fatal: the route still serves,
	// with signature validation off, which is what it did before a secret
	// was ever configurable. It is logged so the degradation is visible.
	whSecret, err := resolveWebhookSecret(proj.webhook, secrets)
	if err != nil {
		log.Error("webhook secret unresolvable; starting with signature validation disabled",
			"engine", proj.webhook.Secret.Engine, "key", proj.webhook.Secret.Key, "err", err)
	}

	return ResolvedConfig{
		Options:       o,
		TelegramToken: tgToken,
		Telegram:      proj.telegram,
		Email:         proj.email,
		Webhook:       proj.webhook,
		WebhookSecret: whSecret,
		WebhookAddr:   proj.webhookAddr,
		WorkDir:       proj.workDir,
		BotUser:       proj.botUser,
		HealthURL:     proj.healthURL,
		ShowToolCalls: proj.showToolCalls,
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
		stateStore:    ServiceTarget{Target: cfg.Services.Get(config.ServiceNameState).Target, Token: cfg.Services.Get(config.ServiceNameState).TargetToken},
		telegram:      cfg.Chat.Telegram,
		email:         cfg.Chat.Email,
		webhook:       cfg.Chat.Webhook,
		webhookAddr:   cfg.Chat.WebhookAddr,
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

// resolveTelegramToken reads whichever credential source is configured. A
// secret ref takes precedence over token_env, so a deployment that moved to
// a secret engine is not silently served by a stale environment variable.
func resolveTelegramToken(cfg config.TelegramConfig, secrets *secret.Registry) (string, error) {
	if cfg.Token != (secret.SecretRef{}) {
		return secrets.Resolve(cfg.Token)
	}
	if cfg.TokenEnv != "" {
		return os.Getenv(cfg.TokenEnv), nil
	}
	return "", nil
}

func resolveWebhookSecret(route config.WebhookRoute, secrets *secret.Registry) (string, error) {
	if route.Secret != (secret.SecretRef{}) {
		return secrets.Resolve(route.Secret)
	}
	return "", nil
}
