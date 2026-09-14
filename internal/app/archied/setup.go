package archied

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	configtemplate "github.com/samcharles93/archie-core"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/setup"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/tomlwrite"
	"github.com/samcharles93/archie-core/internal/infrastructure/terminalprompt"
)

// setupCommand is the argv[1] token that switches archied out of daemon
// mode and into the first-run configuration generator.
const setupCommand = "setup"

// defaultsParams is the known-valid unattended baseline --defaults supplies.
// Answering every prompt with its own default does NOT produce a loadable
// config (the provider step's first option is OpenAI, whose model prompt
// hard-errors on an empty answer), so this covers every required answer and
// leaves the optional ones to answer themselves as "skip": ollama is the
// only keyless provider, so an unattended install boots without a secret.
func defaultsParams() setup.Params {
	return setup.Params{
		BotUser:   "archie-bot",
		ForgeType: "github",
		ForgeHost: "https://github.com",
		Provider:  "ollama",
		Model:     "llama3",
	}
}

// IsSetupArgs reports whether args (os.Args[1:]) selects setup rather than
// the daemon.
func IsSetupArgs(args []string) bool {
	return len(args) > 0 && args[0] == setupCommand
}

// setupFlags is the parameterised answer surface, kept apart from RunSetup so
// the flag definitions, the mapping into setup.Params and the flow stay
// independently readable. Nothing here reads a terminal or the environment:
// parameters and prompts are the only two ways an answer arrives, and no
// environment variable is consulted.
type setupFlags struct {
	cfgPath           string
	defaults          bool
	botUser           string
	operator          string
	forgeType         string
	forgeHost         string
	provider          string
	model             string
	telegramUserIDs   string
	forgeSecretRef    string
	providerSecretRef string
	telegramSecretRef string
}

func bindSetupFlags(fs *flag.FlagSet) *setupFlags {
	f := &setupFlags{}
	fs.StringVar(&f.cfgPath, "config", filepath.Join(configHome(), "archie", "config.toml"), "path to write config.toml")
	fs.BoolVar(&f.defaults, "defaults", false, "write a known-valid unattended baseline config without prompting")
	fs.StringVar(&f.botUser, "bot-user", "", "forge username for archied's commits and API calls")
	fs.StringVar(&f.operator, "operator", "", "operator display name (optional)")
	fs.StringVar(&f.forgeType, "forge-type", "", "forge type: github | gitea | none")
	fs.StringVar(&f.forgeHost, "forge-host", "", "forge base URL")
	fs.StringVar(&f.provider, "provider", "", "LLM provider class: openai | anthropic | openrouter | gemini | groq | deepseek | mistral | ollama")
	fs.StringVar(&f.model, "model", "", "bare model name; the provider class prefix is added automatically")
	fs.StringVar(&f.telegramUserIDs, "telegram-user-ids", "", "comma-separated allowed Telegram user IDs; configuring Telegram requires at least one")
	// References, not values: engine:key names where a secret already lives, and
	// neither part is sensitive. A value has no flag, deliberately -- see Params.
	fs.StringVar(&f.forgeSecretRef, "forge-secret-ref", "", "engine:key naming where the forge token is stored (e.g. bws:ARCHIE_GITHUB_TOKEN); that engine must be configured")
	fs.StringVar(&f.providerSecretRef, "provider-secret-ref", "", "engine:key naming where the provider API key is stored (e.g. bws:OPENAI_API_KEY); that engine must be configured")
	fs.StringVar(&f.telegramSecretRef, "telegram-secret-ref", "", "engine:key naming where the Telegram bot token is stored (e.g. bws:TELEGRAM_BOT_TOKEN); that engine must be configured")
	return f
}

// params folds the flags over the baseline. A flag always wins; without
// -defaults the baseline is empty, so only the flags pre-answer anything and
// every other question is prompted for.
func (f *setupFlags) params() (setup.Params, error) {
	var params setup.Params
	if f.defaults {
		params = defaultsParams()
	}
	if f.botUser != "" {
		params.BotUser = f.botUser
	}
	if f.operator != "" {
		params.Operator = f.operator
	}
	if f.forgeType != "" {
		params.ForgeType = f.forgeType
	}
	if f.forgeHost != "" {
		params.ForgeHost = f.forgeHost
	}
	if f.provider != "" {
		params.Provider = f.provider
	}
	if f.model != "" {
		params.Model = f.model
	}
	// Parsed at the boundary, so a malformed reference fails before anything is
	// rendered or written.
	for _, ref := range []struct {
		flag  string
		value string
		dst   *config.SecretRef
	}{
		{"-forge-secret-ref", f.forgeSecretRef, &params.ForgeTokenRef},
		{"-provider-secret-ref", f.providerSecretRef, &params.ProviderAPIKeyRef},
		{"-telegram-secret-ref", f.telegramSecretRef, &params.TelegramTokenRef},
	} {
		if ref.value == "" {
			continue
		}
		parsed, err := setup.ParseSecretRef(ref.value)
		if err != nil {
			return setup.Params{}, fmt.Errorf("%s: %w", ref.flag, err)
		}
		*ref.dst = parsed
	}

	if f.telegramUserIDs != "" {
		ids, err := setup.ParseTelegramUserIDs(f.telegramUserIDs)
		if err != nil {
			return setup.Params{}, fmt.Errorf("-telegram-user-ids: %w", err)
		}
		params.TelegramUserIDs = ids
	}
	return params, nil
}

// setupPrompter picks the answer surface. -defaults answers every remaining
// question with its own default and never touches a terminal; otherwise stdin
// must be a real terminal, and terminalprompt refuses it otherwise rather than
// silently generating a config nobody chose.
func setupPrompter(defaults bool, stdin *os.File, stdout io.Writer) (setup.Prompter, error) {
	if defaults {
		return setup.DefaultPrompter{}, nil
	}
	return terminalprompt.New(stdin, stdout)
}

// existingConfig reads the config being replaced, returning the bytes to apply
// edits to and the values worth prefilling prompts with. A missing file is a
// fresh install, not an error. A file that exists but does not load is still
// edited in place: tomlwrite only rewrites the lines it owns, and refusing
// would strand an operator whose config is malformed.
func existingConfig(loader *configuration.Loader, path string) ([]byte, setup.ExistingValues, error) {
	base, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, setup.ExistingValues{}, nil
	}
	if err != nil {
		return nil, setup.ExistingValues{}, fmt.Errorf("read existing config: %w", err)
	}
	var existing setup.ExistingValues
	if doc, err := loader.File(path); err == nil {
		existing = setup.ExistingValues{
			BotUser:   doc.Config.BotUser,
			Operator:  doc.Config.Chat.Operator,
			ForgeHost: doc.Config.Forge.Host,
		}
	}
	return base, existing, nil
}

// RunSetup generates (or re-generates) a config.toml, validates it through a
// real configuration.Loader, and only then writes it atomically with mode
// 0600 and commits any secrets to the env file beside it. --defaults uses a
// known-valid unattended baseline and never touches a terminal; without it,
// stdin must be a real TTY and the guided flow runs.
func RunSetup(args []string, stdin *os.File, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(setupCommand, flag.ContinueOnError)
	fs.SetOutput(stderr)
	flags := bindSetupFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "setup: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	params, err := flags.params()
	if err != nil {
		fmt.Fprintf(stderr, "setup: %v\n", err)
		return 2
	}
	p, err := setupPrompter(flags.defaults, stdin, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "setup: %v\n", err)
		return 1
	}

	cfgDir := filepath.Dir(flags.cfgPath)
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		fmt.Fprintf(stderr, "setup: create config directory: %v\n", err)
		return 1
	}
	sink := setup.NewEnvFileSink(filepath.Join(cfgDir, "env"))
	loader := configuration.New(nil)
	base, existing, err := existingConfig(loader, flags.cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "setup: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	edits, err := setup.RunParams(ctx, p, nil, sink, existing, params)
	if err != nil {
		fmt.Fprintf(stderr, "setup: %v\n", err)
		return 1
	}

	var rendered []byte
	if base != nil {
		rendered, err = tomlwrite.Apply(base, edits)
	} else {
		rendered, err = tomlwrite.Generate(configtemplate.Example, edits)
	}
	if err != nil {
		fmt.Fprintf(stderr, "setup: render config: %v\n", err)
		return 1
	}

	if err := installValidatedConfig(loader, flags.cfgPath, sink, rendered); err != nil {
		fmt.Fprintf(stderr, "setup: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "setup: wrote %s\n", flags.cfgPath)
	return 0
}

// installValidatedConfig proves rendered loads through a real Loader, then
// writes the config atomically with mode 0600 and only then commits the
// secret sink. A validation failure returns before either write, so a bad
// generated config never leaves a config file or a secret on disk, and a
// secret is never committed pointing at a config that was never written.
func installValidatedConfig(loader *configuration.Loader, path string, sink setup.SecretSink, rendered []byte) error {
	if err := proveLoadable(loader, rendered); err != nil {
		return err
	}
	if err := writeFileAtomicMode(path, rendered, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := sink.Commit(); err != nil {
		return fmt.Errorf("commit secrets: %w", err)
	}
	return nil
}

// proveLoadable decodes and validates rendered through a real
// configuration.Loader, the same check setup.Run's doc comment makes the
// caller's responsibility. The rendered bytes are staged in a temp file
// rather than the destination, so a load failure can never have already
// installed anything.
func proveLoadable(loader *configuration.Loader, rendered []byte) error {
	tmp, err := os.CreateTemp("", "archie-setup-*.toml")
	if err != nil {
		return fmt.Errorf("prove config loads: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("prove config loads: %w", err)
	}
	if err := os.WriteFile(name, rendered, 0o600); err != nil {
		return fmt.Errorf("prove config loads: %w", err)
	}
	if _, err := loader.File(name); err != nil {
		return fmt.Errorf("generated config does not load: %w", err)
	}
	return nil
}

// writeFileAtomicMode writes data to path via a same-directory temp file and
// rename, so a concurrent reader never observes a truncated config.
func writeFileAtomicMode(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
