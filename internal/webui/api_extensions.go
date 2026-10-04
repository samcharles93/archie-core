package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
)

type authorityView struct {
	CredentialServices []string `json:"credential_services"`
	EgressHosts        []string `json:"egress_hosts"`
	ForgePermissions   []string `json:"forge_permissions"`
	Triggers           []string `json:"triggers"`
	Tools              []string `json:"tools"`
	Env                []string `json:"env"`
}

func authorityOf(a storepkg.Authority) authorityView {
	return authorityView{
		CredentialServices: orEmpty(a.CredentialServices), EgressHosts: orEmpty(a.EgressHosts),
		ForgePermissions: orEmpty(a.ForgePermissions), Triggers: orEmpty(a.Triggers),
		Tools: orEmpty(a.Tools), Env: orEmpty(a.Env),
	}
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ExtensionView is one installed package as the dashboard shows it: what it
// declares, what the operator accepted, and whether it is enabled.
type ExtensionView struct {
	Name        string         `json:"name"`
	DisplayName string         `json:"display_name"`
	Description string         `json:"description"`
	Version     string         `json:"version"`
	Reference   string         `json:"reference"`
	Digest      string         `json:"digest"`
	Surfaces    []string       `json:"surfaces"`
	Declared    authorityView  `json:"declared"`
	Accepted    *authorityView `json:"accepted"`
	Enabled     bool           `json:"enabled"`
}

func extensionView(p storepkg.Installed, enabled bool) ExtensionView {
	view := ExtensionView{
		Name: p.Name, DisplayName: p.Descriptor.DisplayName, Description: p.Descriptor.Description,
		Version: p.Descriptor.Version, Reference: p.Reference, Digest: p.Digest,
		Surfaces: []string{}, Declared: authorityOf(p.Descriptor.Authority), Enabled: enabled,
	}
	for _, extension := range p.Descriptor.Contributes.Extensions {
		if !slices.Contains(view.Surfaces, extension.Surface) {
			view.Surfaces = append(view.Surfaces, extension.Surface)
		}
	}
	if p.AcceptedAuthority != nil {
		accepted := authorityOf(*p.AcceptedAuthority)
		view.Accepted = &accepted
	}
	return view
}

// extensionSettings reads the extension-settings resource and the version a
// replacement must name.
func (s *Server) extensionSettings(ctx context.Context) (controlplanerpc.ExtensionSettings, int64, error) {
	var doc controlplanerpc.ExtensionSettings
	response, err := s.ControlPlane.Query(ctx, &controlpb.QueryRequest{Kind: controlplanerpc.ExtensionSettingsKind})
	if err != nil {
		return doc, 0, err
	}
	if response.Resource == nil {
		return doc, 0, errors.New("extension settings missing")
	}
	if err := json.Unmarshal(response.Resource.ValueJson, &doc); err != nil {
		return doc, 0, err
	}
	return doc, response.Resource.Version, nil
}

// putExtensionSettings replaces the extension-settings resource at version.
func (s *Server) putExtensionSettings(ctx context.Context, doc controlplanerpc.ExtensionSettings, version int64) error {
	value, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	audit, err := webAudit()
	if err != nil {
		return err
	}
	_, err = s.ControlPlane.Command(ctx, &controlpb.CommandRequest{
		Kind: controlplanerpc.ExtensionSettingsKind, Command: "replace", ValueJson: value,
		ExpectedVersion: version, Actor: string(audit.ActorID), Source: audit.Source, RequestId: audit.RequestID,
	})
	return err
}

// withExtension returns doc with the named extension set to enabled, or
// removed when enabled is nil.
func withExtension(doc controlplanerpc.ExtensionSettings, name string, enabled *bool) controlplanerpc.ExtensionSettings {
	next := controlplanerpc.ExtensionSettings{}
	found := false
	for _, setting := range doc.Extensions {
		if setting.Name != name {
			next.Extensions = append(next.Extensions, setting)
			continue
		}
		found = true
		if enabled != nil {
			setting.Enabled = *enabled
			next.Extensions = append(next.Extensions, setting)
		}
	}
	if !found && enabled != nil {
		next.Extensions = append(next.Extensions, controlplanerpc.ExtensionSetting{Name: name, Enabled: *enabled})
	}
	return next
}

func (s *Server) extensionsReady(w http.ResponseWriter) bool {
	if s.Packages == nil || s.ControlPlane == nil {
		http.Error(w, "extensions unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func writePackageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storepkg.ErrNotFound):
		http.Error(w, "extension not installed", http.StatusNotFound)
	case errors.Is(err, storepkg.ErrInstalled):
		http.Error(w, "extension already installed", http.StatusConflict)
	default:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}

// handleExtensions lists the installed packages with their authority and
// enabled state.
func (s *Server) handleExtensions(w http.ResponseWriter, r *http.Request) {
	if !s.extensionsReady(w) {
		return
	}
	installed, err := s.Packages.ListInstalled(r.Context())
	if err != nil {
		writePackageError(w, err)
		return
	}
	doc, _, err := s.extensionSettings(r.Context())
	if err != nil {
		writeControlPlaneError(w, err)
		return
	}
	enabled := make(map[string]bool, len(doc.Extensions))
	for _, setting := range doc.Extensions {
		enabled[setting.Name] = setting.Enabled
	}
	views := make([]ExtensionView, 0, len(installed))
	for _, p := range installed {
		views = append(views, extensionView(p, enabled[p.Name]))
	}
	writeJSON(w, map[string]any{"extensions": views})
}

type installExtensionRequest struct {
	Name      string `json:"name"`
	Reference string `json:"reference"`
	Digest    string `json:"digest"`
}

// handleExtensionInstall installs a digest-pinned package. It does not enable
// it and does not accept its authority: both are separate operator actions.
func (s *Server) handleExtensionInstall(w http.ResponseWriter, r *http.Request) {
	if !s.extensionsReady(w) {
		return
	}
	var request installExtensionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	p, err := s.Packages.InstallPackage(r.Context(),
		strings.TrimSpace(request.Name), strings.TrimSpace(request.Reference), strings.TrimSpace(request.Digest))
	if err != nil {
		writePackageError(w, err)
		return
	}
	writeJSON(w, extensionView(p, false))
}

// handleExtensionAccept records the operator's acceptance of exactly the
// authority the package declares.
func (s *Server) handleExtensionAccept(w http.ResponseWriter, r *http.Request) {
	if !s.extensionsReady(w) {
		return
	}
	name := r.PathValue("name")
	installed, err := s.Packages.GetInstalled(r.Context(), name)
	if err != nil {
		writePackageError(w, err)
		return
	}
	p, err := s.Packages.AcceptPackageAuthority(r.Context(), name, installed.Descriptor.Authority)
	if err != nil {
		writePackageError(w, err)
		return
	}
	writeJSON(w, extensionView(p, false))
}

type extensionEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

// handleExtensionEnabled enables or disables an installed extension.
func (s *Server) handleExtensionEnabled(w http.ResponseWriter, r *http.Request) {
	if !s.extensionsReady(w) {
		return
	}
	var request extensionEnabledRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	name := r.PathValue("name")
	if _, err := s.Packages.GetInstalled(r.Context(), name); err != nil {
		writePackageError(w, err)
		return
	}
	doc, version, err := s.extensionSettings(r.Context())
	if err != nil {
		writeControlPlaneError(w, err)
		return
	}
	if err := s.putExtensionSettings(r.Context(), withExtension(doc, name, &request.Enabled), version); err != nil {
		writeControlPlaneError(w, err)
		return
	}
	writeJSON(w, map[string]any{"name": name, "enabled": request.Enabled})
}

// handleExtensionRemove uninstalls an extension and drops its setting, so a
// later reinstall does not start enabled.
func (s *Server) handleExtensionRemove(w http.ResponseWriter, r *http.Request) {
	if !s.extensionsReady(w) {
		return
	}
	name := r.PathValue("name")
	if err := s.Packages.RemoveInstalled(r.Context(), name); err != nil {
		writePackageError(w, err)
		return
	}
	if doc, version, err := s.extensionSettings(r.Context()); err == nil {
		if err := s.putExtensionSettings(r.Context(), withExtension(doc, name, nil), version); err != nil {
			s.Log.Warn("drop extension setting", "name", name, "err", err)
		}
	}
	writeJSON(w, map[string]any{"name": name, "removed": true})
}
