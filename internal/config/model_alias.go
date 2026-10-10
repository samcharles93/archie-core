package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// ModelPurpose is what a model is used for. Each purpose has a default alias
// that an empty alias resolves to.
type ModelPurpose string

const (
	PurposeAgent         ModelPurpose = "agent"
	PurposeChat          ModelPurpose = "chat"
	PurposeEmbedding     ModelPurpose = "embedding"
	PurposeTranscription ModelPurpose = "transcription"
)

// DefaultModelAlias is the alias agent steps and chat use when none is named.
const DefaultModelAlias = "default"

// DefaultAlias is the alias an empty alias means for purpose.
func (p ModelPurpose) DefaultAlias() string {
	switch p {
	case PurposeEmbedding, PurposeTranscription:
		return string(p)
	default:
		return DefaultModelAlias
	}
}

// ResolveModel returns the "provider/model" ref aliases maps alias to, or
// purpose's default alias when alias is empty. An unknown alias is an error,
// never a substitute.
func ResolveModel(aliases map[string]string, alias string, purpose ModelPurpose) (string, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		alias = purpose.DefaultAlias()
	}
	ref := strings.TrimSpace(aliases[alias])
	if ref == "" {
		return "", fmt.Errorf("no model alias %q is configured (Settings > Models)", alias)
	}
	return ref, nil
}

// ChatAliases are the aliases chat may switch between: every alias but the
// embedding and transcription ones, sorted. Never nil.
func ChatAliases(aliases map[string]string) []string {
	out := make([]string, 0, len(aliases))
	for _, alias := range slices.Sorted(maps.Keys(aliases)) {
		if alias != string(PurposeEmbedding) && alias != string(PurposeTranscription) {
			out = append(out, alias)
		}
	}
	return out
}
