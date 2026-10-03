// Package storepkg defines and validates declarative Archie store packages.
package storepkg

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const APIVersion = "dev.archie.package.v1"

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// Descriptor is the dev.archie.package.v1 manifest annotation payload.
type Descriptor struct {
	APIVersion  string        `yaml:"apiVersion"`
	DisplayName string        `yaml:"displayName"`
	Description string        `yaml:"description"`
	Version     string        `yaml:"version"`
	Contributes Contributions `yaml:"contributes"`
	Requires    []PackageRef  `yaml:"requires"`
	Authority   Authority     `yaml:"authority"`
	Files       []File        `yaml:"files"`
}

// Contributions names the declarative families supplied by a package.
type Contributions struct {
	Workflows  []string `yaml:"workflows"`
	Prompts    []string `yaml:"prompts"`
	Skills     []string `yaml:"skills"`
	MCPServers []string `yaml:"mcpServers"`
	Defaults   []string `yaml:"defaults"`
	// Extensions are executable plugins the host launches as processes. They
	// are the only package files allowed to carry an executable mode.
	Extensions []Extension `yaml:"extensions"`
}

// Extension is one plugin binary in the package layer and the gRPC surface it
// serves.
type Extension struct {
	Surface string `yaml:"surface"`
	Path    string `yaml:"path"`
}

// Surfaces are the extension surfaces the host defines a contract for.
const SurfaceSecretEngine = "secretengine"

func validSurface(surface string) bool { return surface == SurfaceSecretEngine }

// PackageRef identifies a required package or Kit. References are pinned to
// immutable content digests so dependency resolution cannot drift by tag.
type PackageRef struct {
	Name   string `yaml:"name"`
	Digest string `yaml:"digest"`
}

// Authority lists the grants the package needs to operate.
type Authority struct {
	CredentialServices []string `yaml:"credentialServices"`
	EgressHosts        []string `yaml:"egressHosts"`
	ForgePermissions   []string `yaml:"forgePermissions"`
	Triggers           []string `yaml:"triggers"`
	Tools              []string `yaml:"tools"`
	// Env names the host environment variables an extension process may read.
	Env []string `yaml:"env"`
}

// File describes one path carried in the package layer and its Unix mode.
type File struct {
	Path string `yaml:"path"`
	Mode uint32 `yaml:"mode"`
}

// Decode strictly decodes one YAML document and validates the descriptor.
// Unknown fields and additional YAML documents are errors.
func Decode(r io.Reader) (Descriptor, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Descriptor{}, fmt.Errorf("read package descriptor: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var descriptor Descriptor
	if err := decoder.Decode(&descriptor); err != nil {
		return Descriptor{}, fmt.Errorf("decode package descriptor: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return Descriptor{}, fmt.Errorf("decode trailing package descriptor: %w", err)
		}
		return Descriptor{}, fmt.Errorf("decode package descriptor: multiple YAML documents are not allowed")
	}
	if err := descriptor.Validate(); err != nil {
		return Descriptor{}, err
	}
	return descriptor, nil
}

// Validate checks descriptor identity, references, authority values, and
// that every listed package file remains declarative and non-executable, except
// the binaries a package declares as extensions.
func (d Descriptor) Validate() error {
	if d.APIVersion != APIVersion {
		return fmt.Errorf("apiVersion must be %q", APIVersion)
	}
	if strings.TrimSpace(d.DisplayName) == "" {
		return fmt.Errorf("display name is required")
	}
	if strings.TrimSpace(d.Version) == "" {
		return fmt.Errorf("version is required")
	}
	if !d.hasContribution() {
		return fmt.Errorf("at least one package contribution is required")
	}
	for i, ref := range d.Requires {
		if strings.TrimSpace(ref.Name) == "" {
			return fmt.Errorf("required package %d: name is required", i)
		}
		if !digestPattern.MatchString(ref.Digest) {
			return fmt.Errorf("required package %q digest must be a sha256 digest", ref.Name)
		}
	}
	if err := d.Authority.Validate(); err != nil {
		return err
	}
	executable, err := d.validateExtensions()
	if err != nil {
		return err
	}
	for i, file := range d.Files {
		if err := validateFile(file, executable[file.Path]); err != nil {
			return fmt.Errorf("package file %d: %w", i, err)
		}
	}
	return nil
}

// validateExtensions checks each extension names a known surface and a
// declared, owner-executable file, and returns the set of extension paths.
func (d Descriptor) validateExtensions() (map[string]bool, error) {
	modes := make(map[string]uint32, len(d.Files))
	for _, file := range d.Files {
		modes[file.Path] = file.Mode
	}
	paths := make(map[string]bool, len(d.Contributes.Extensions))
	for i, extension := range d.Contributes.Extensions {
		if !validSurface(extension.Surface) {
			return nil, fmt.Errorf("extension %d: unknown surface %q", i, extension.Surface)
		}
		mode, ok := modes[extension.Path]
		if !ok {
			return nil, fmt.Errorf("extension %d: %q is not a declared package file", i, extension.Path)
		}
		if mode&0o100 == 0 {
			return nil, fmt.Errorf("extension %d: %q must be owner-executable", i, extension.Path)
		}
		if paths[extension.Path] {
			return nil, fmt.Errorf("extension %d: %q is listed twice", i, extension.Path)
		}
		paths[extension.Path] = true
	}
	return paths, nil
}

func (d Descriptor) hasContribution() bool {
	return len(d.Contributes.Workflows)+len(d.Contributes.Prompts)+len(d.Contributes.Skills)+
		len(d.Contributes.MCPServers)+len(d.Contributes.Defaults)+len(d.Contributes.Extensions) > 0
}

func validForgePermission(permission string) bool {
	switch permission {
	case "read", "comment", "review", "push", "open_pr":
		return true
	default:
		return false
	}
}

func validateFile(file File, extension bool) error {
	clean := path.Clean(file.Path)
	if file.Path == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(file.Path) {
		return fmt.Errorf("path must be a relative package path")
	}
	if extension {
		return nil
	}
	if file.Mode&0o111 != 0 {
		return fmt.Errorf("executable file modes are not allowed")
	}
	if isHostCodePath(clean) {
		return fmt.Errorf("host code is not declarative package content")
	}
	return nil
}

func isHostCodePath(file string) bool {
	ext := strings.ToLower(path.Ext(file))
	switch ext {
	case ".go", ".sh", ".bash", ".zsh", ".fish", ".ksh", ".csh",
		".py", ".pyw", ".rb", ".pl", ".pm", ".php", ".lua", ".tcl", ".r",
		".js", ".mjs", ".cjs", ".ts", ".bat", ".cmd", ".ps1":
		return true
	default:
		return false
	}
}
