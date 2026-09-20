package messaging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/identity"
)

var (
	ErrSettingsValidation  = errors.New("settings validation")
	ErrSettingsNotFound    = errors.New("settings not found")
	ErrSettingsConflict    = errors.New("settings version conflict")
	ErrSettingsUnavailable = errors.New("settings unavailable")
)

type SettingField struct {
	Type   string
	Secret bool
}

type SettingDescriptor struct {
	Kind   string
	Title  string
	Fields map[string]SettingField
}

type SettingResource struct {
	Kind    string
	Version int64
	Value   map[string]any
	Raw     any
}

type SettingCommand struct {
	Kind            string
	Value           map[string]any
	RawValue        any
	ExpectedVersion int64
	Actor           string
	Source          string
	RequestID       string
}

type SettingsClient interface {
	Catalog(context.Context) ([]SettingDescriptor, error)
	Query(context.Context, string) (SettingResource, error)
	Command(context.Context, SettingCommand) (SettingResource, error)
}

type SettingsCommand struct {
	client     SettingsClient
	identities identity.Repository
}

func NewSettingsCommand(client SettingsClient) *SettingsCommand {
	return &SettingsCommand{client: client}
}

func (c *SettingsCommand) WithIdentities(repository identity.Repository) *SettingsCommand {
	c.identities = repository
	return c
}

const settingsUsage = "Usage: /settings [list|get <kind>|set <kind> <field>=<value> ...|replace <kind> <json>]"

func (c *SettingsCommand) Execute(ctx context.Context, actor, input string) string {
	if c == nil || c.client == nil {
		return "Settings are unavailable."
	}
	args, err := splitSettingsArgs(strings.TrimSpace(input))
	if err != nil {
		return "Invalid settings command: " + err.Error() + ". " + settingsUsage
	}
	if len(args) == 0 || args[0] == "help" {
		return settingsUsage
	}
	switch args[0] {
	case "list":
		return c.list(ctx, args)
	case "get":
		return c.get(ctx, args)
	case "set":
		return c.set(ctx, actor, args)
	case "identity":
		return c.identity(ctx, actor, args[1:])
	case "replace":
		return c.replace(ctx, actor, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), "replace")))
	default:
		return settingsUsage
	}
}

// resolveActor maps a channel sender to the Archie identity the write is
// recorded against. A sender that does not resolve cannot run a command: the
// audit record names an identity or the command does not happen.
func (c *SettingsCommand) resolveActor(ctx context.Context, subject, actor string) (identity.IdentityID, string) {
	if strings.TrimSpace(actor) == "" {
		return "", subject + " require an authenticated Archie identity; this channel cannot resolve one."
	}
	if c.identities == nil {
		return "", subject + " require an authenticated Archie identity; identity administration is unavailable."
	}
	resolved, err := c.identities.ResolveLegacyName(ctx, actor)
	if err != nil {
		return "", subject + " require an authenticated Archie identity; this sender did not resolve."
	}
	return resolved.ID, ""
}

func (c *SettingsCommand) replace(ctx context.Context, actor, input string) string {
	actorID, refusal := c.resolveActor(ctx, "Settings changes", actor)
	if refusal != "" {
		return refusal
	}
	kind, raw, ok := strings.Cut(strings.TrimSpace(input), " ")
	if !ok || kind == "" || strings.TrimSpace(raw) == "" {
		return settingsUsage
	}
	if _, err := c.descriptor(ctx, kind); err != nil {
		return settingsError(err)
	}
	current, err := c.client.Query(ctx, kind)
	if err != nil {
		return settingsError(err)
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return "Settings validation failed: invalid JSON."
	}
	updated, err := c.client.Command(ctx, SettingCommand{Kind: kind, RawValue: value, ExpectedVersion: current.Version, Actor: string(actorID), Source: "messaging", RequestID: newSettingsRequestID()})
	if err != nil {
		return settingsError(err)
	}
	return fmt.Sprintf("Updated %s to version %d.", updated.Kind, updated.Version)
}

func (c *SettingsCommand) identity(ctx context.Context, actor string, args []string) string { //nolint:gocyclo,cyclop,funlen // command arity checks remain adjacent to spoof-sensitive dispatch
	const usage = "Usage: /settings identity <list|create <kind> <name>|rename <id> <name>|suspend <id>|reactivate <id>|retire <id>>"
	actorID, refusal := c.resolveActor(ctx, "Identity changes", actor)
	if refusal != "" {
		return refusal
	}
	if len(args) == 1 && args[0] == "list" {
		values, listErr := c.identities.List(ctx)
		if listErr != nil {
			return "Identity administration is unavailable: " + listErr.Error()
		}
		lines := []string{"Identities:"}
		for _, value := range values {
			lines = append(lines, fmt.Sprintf("- %s %s %s %q", value.ID, value.Kind, value.Lifecycle, value.DisplayName))
		}
		return strings.Join(lines, "\n")
	}
	audit := identity.Audit{ActorID: actorID, Source: "messaging", RequestID: newSettingsRequestID()}
	if len(args) == 3 && args[0] == "create" {
		id := identity.StableID(audit.RequestID)
		value, createErr := identity.New(id, identity.Kind(args[1]), args[2])
		if createErr == nil {
			value, createErr = c.identities.Create(ctx, value, audit)
		}
		if createErr != nil {
			return "Identity change failed: " + createErr.Error()
		}
		return fmt.Sprintf("Created %s %q.", value.ID, value.DisplayName)
	}
	if len(args) < 2 {
		return usage
	}
	target, getErr := c.identities.Get(ctx, identity.IdentityID(args[1]))
	if getErr != nil {
		return "Identity change failed: " + getErr.Error()
	}
	var command identity.Command
	switch args[0] {
	case "rename":
		if len(args) != 3 {
			return usage
		}
		command = identity.Command{Type: identity.CommandRename, DisplayName: args[2]}
	case "suspend":
		if len(args) != 2 {
			return usage
		}
		command.Type = identity.CommandSuspend
	case "reactivate":
		if len(args) != 2 {
			return usage
		}
		command.Type = identity.CommandReactivate
	case "retire":
		if len(args) != 2 {
			return usage
		}
		command.Type = identity.CommandRetire
	default:
		return usage
	}
	updated, applyErr := c.identities.Apply(ctx, target.ID, target.Version, command, audit)
	if applyErr != nil {
		return "Identity change failed: " + applyErr.Error()
	}
	return fmt.Sprintf("%s is %s (version %d).", updated.DisplayName, updated.Lifecycle, updated.Version)
}

func (c *SettingsCommand) list(ctx context.Context, args []string) string {
	if len(args) != 1 {
		return settingsUsage
	}
	descriptors, err := c.client.Catalog(ctx)
	if err != nil {
		return settingsError(err)
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].Kind < descriptors[j].Kind })
	lines := []string{"Settings:"}
	for _, descriptor := range descriptors {
		lines = append(lines, fmt.Sprintf("- %s — %s", descriptor.Kind, descriptor.Title))
	}
	return strings.Join(lines, "\n")
}

func (c *SettingsCommand) get(ctx context.Context, args []string) string {
	if len(args) != 2 {
		return settingsUsage
	}
	descriptor, err := c.descriptor(ctx, args[1])
	if err != nil {
		return settingsError(err)
	}
	resource, err := c.client.Query(ctx, args[1])
	if err != nil {
		return settingsError(err)
	}
	return renderSetting(resource, descriptor)
}

func (c *SettingsCommand) set(ctx context.Context, actor string, args []string) string {
	if len(args) < 3 {
		return settingsUsage
	}
	actorID, refusal := c.resolveActor(ctx, "Settings changes", actor)
	if refusal != "" {
		return refusal
	}
	descriptor, err := c.descriptor(ctx, args[1])
	if err != nil {
		return settingsError(err)
	}
	current, err := c.client.Query(ctx, args[1])
	if err != nil {
		return settingsError(err)
	}
	value := cloneSettingValue(current.Value)
	for _, assignment := range args[2:] {
		name, raw, ok := strings.Cut(assignment, "=")
		field, exists := descriptor.Fields[name]
		if !ok || name == "" || !exists || field.Secret {
			return fmt.Sprintf("Unknown or non-editable field %q for %s.", name, descriptor.Kind)
		}
		parsed, parseErr := parseSettingValue(field.Type, raw)
		if parseErr != nil {
			return fmt.Sprintf("Invalid value for %s: %v.", name, parseErr)
		}
		value[name] = parsed
	}
	updated, err := c.client.Command(ctx, SettingCommand{Kind: current.Kind, Value: value, ExpectedVersion: current.Version, Actor: string(actorID), Source: "messaging", RequestID: newSettingsRequestID()})
	if err != nil {
		return settingsError(err)
	}
	return fmt.Sprintf("Updated %s to version %d.\n%s", updated.Kind, updated.Version, renderSetting(updated, descriptor))
}

func (c *SettingsCommand) descriptor(ctx context.Context, kind string) (SettingDescriptor, error) {
	descriptors, err := c.client.Catalog(ctx)
	if err != nil {
		return SettingDescriptor{}, err
	}
	for _, descriptor := range descriptors {
		if descriptor.Kind == kind {
			return descriptor, nil
		}
	}
	return SettingDescriptor{}, ErrSettingsNotFound
}

func parseSettingValue(kind, raw string) (any, error) {
	switch kind {
	case "boolean":
		return strconv.ParseBool(raw)
	case "integer":
		return strconv.ParseInt(raw, 10, 64)
	case "duration":
		duration, err := time.ParseDuration(raw)
		if err != nil {
			return nil, err
		}
		return int64(duration / time.Second), nil
	case "string":
		return raw, nil
	default:
		return nil, fmt.Errorf("unsupported type %q", kind)
	}
}

func renderSetting(resource SettingResource, descriptor SettingDescriptor) string {
	if resource.Value == nil {
		value, err := json.MarshalIndent(resource.Raw, "", "  ")
		if err != nil {
			return fmt.Sprintf("%s (version %d)", resource.Kind, resource.Version)
		}
		return fmt.Sprintf("%s (version %d):\n%s", resource.Kind, resource.Version, value)
	}
	names := make([]string, 0, len(resource.Value))
	for name := range resource.Value {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := []string{fmt.Sprintf("%s (version %d):", resource.Kind, resource.Version)}
	for _, name := range names {
		field := descriptor.Fields[name]
		if field.Secret || looksSecret(name) {
			lines = append(lines, name+"=<redacted>")
			continue
		}
		lines = append(lines, fmt.Sprintf("%s=%v", name, resource.Value[name]))
	}
	return strings.Join(lines, "\n")
}

func looksSecret(name string) bool {
	name = strings.ToLower(name)
	for _, marker := range []string{"secret", "password", "token", "credential", "api_key", "private_key"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func cloneSettingValue(value map[string]any) map[string]any {
	cloned := make(map[string]any, len(value))
	maps.Copy(cloned, value)
	return cloned
}

func newSettingsRequestID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("messaging-%d", time.Now().UnixNano())
	}
	return "messaging-" + hex.EncodeToString(bytes[:])
}

func settingsError(err error) string {
	switch {
	case errors.Is(err, ErrSettingsValidation):
		return "Settings validation failed: " + err.Error()
	case errors.Is(err, ErrSettingsNotFound):
		return "Unknown settings kind. Use /settings list."
	case errors.Is(err, ErrSettingsConflict):
		return "Settings version conflict; inspect the latest value and retry."
	default:
		return "Settings are unavailable: " + err.Error()
	}
}

func splitSettingsArgs(input string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			args = append(args, current.String())
			current.Reset()
		}
	}
	for _, char := range input {
		if escaped {
			current.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				current.WriteRune(char)
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			continue
		}
		if char == ' ' || char == '\t' || char == '\n' {
			flush()
			continue
		}
		current.WriteRune(char)
	}
	if escaped || quote != 0 {
		return nil, errors.New("unterminated quote or escape")
	}
	flush()
	return args, nil
}
