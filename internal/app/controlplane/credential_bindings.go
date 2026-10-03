package controlplane

import "github.com/samcharles93/archie-core/internal/config"

// CredentialBindingsKind is the control-plane resource mapping a Kit
// credential@1 service name to an org secret
// It exists so a binding applies without a restart, the same reason
// AgentProfileKind is its own resource: [containers].credentials in
// config.toml is only its seed, and kitrun.Launcher reads the live
// control-plane document on every launch, never a value captured at boot.
const CredentialBindingsKind = "credential-bindings"

// credentialBinding is one config.CredentialBinding as the document writes
// it: explicit snake_case json tags, following channel_settings.go /
// container_settings.go's precedent, never the internal config struct
// (which has no json tags and would fall back to Go field names).
type credentialBinding struct {
	Service string          `json:"service"`
	Org     string          `json:"org,omitempty"`
	Secret  credentialValue `json:"secret"`
}

// credentialValue is config.SecretRef as this document writes it.
type credentialValue struct {
	Engine string `json:"engine,omitempty"`
	Key    string `json:"key,omitempty"`
}

func credentialBindingsDefinition() Definition {
	return Definition{
		Kind:      CredentialBindingsKind,
		Title:     "Credential bindings",
		ApplyMode: "live",
		Document:  []credentialBinding{},
		Seed:      seedCredentialBindings,
		Validate:  validateCredentialBindings,
	}
}

func seedCredentialBindings(cfg config.Config) any {
	return credentialBindingsDocument(cfg.Containers.Credentials)
}

func credentialBindingsDocument(bindings []config.CredentialBinding) []credentialBinding {
	if bindings == nil {
		return nil
	}
	out := make([]credentialBinding, len(bindings))
	for i, b := range bindings {
		out[i] = credentialBinding{Service: b.Service, Org: b.Org, Secret: credentialValue{Engine: b.Secret.Engine, Key: b.Secret.Key}}
	}
	return out
}

// credentialBindingsSettings reverses credentialBindingsDocument, for the
// layering in runtime_config.go and for validation against
// config.ContainerConfig.ValidateCredentialBindings.
func credentialBindingsSettings(bindings []credentialBinding) []config.CredentialBinding {
	if bindings == nil {
		return nil
	}
	out := make([]config.CredentialBinding, len(bindings))
	for i, b := range bindings {
		out[i] = config.CredentialBinding{Service: b.Service, Org: b.Org, Secret: config.SecretRef{Engine: b.Secret.Engine, Key: b.Secret.Key}}
	}
	return out
}

func validateCredentialBindings(input []byte) error {
	// The rules live in the config package and are shared with the file
	// document's own validation (config.ContainerConfig.ValidateCredentialBindings):
	// which bindings are valid cannot differ between the file that seeds this
	// resource and the resource that replaces it.
	return validateAs(input, func(bindings []credentialBinding) error {
		return config.ContainerConfig{Credentials: credentialBindingsSettings(bindings)}.ValidateCredentialBindings()
	})
}
