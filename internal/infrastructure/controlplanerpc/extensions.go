package controlplanerpc

// ExtensionSettingsKind is the control-plane resource kind holding which
// installed extensions are enabled and how each is configured.
const ExtensionSettingsKind = "extension-settings"

// ExtensionSettings is the extension-settings resource document.
type ExtensionSettings struct {
	Extensions []ExtensionSetting `json:"extensions"`
}

// ExtensionSetting enables or disables one installed extension by package name.
// Settings reach the extension through its surface's Configure call.
type ExtensionSetting struct {
	Name     string            `json:"name"`
	Enabled  bool              `json:"enabled"`
	Settings map[string]string `json:"settings,omitempty"`
}
