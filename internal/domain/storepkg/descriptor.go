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
}

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
// that every listed package file remains declarative and non-executable.
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
	for _, permission := range d.Authority.ForgePermissions {
		if !validForgePermission(permission) {
			return fmt.Errorf("invalid forge permission %q", permission)
		}
	}
	for i, file := range d.Files {
		if err := validateFile(file); err != nil {
			return fmt.Errorf("package file %d: %w", i, err)
		}
	}
	return nil
}

func (d Descriptor) hasContribution() bool {
	return len(d.Contributes.Workflows)+len(d.Contributes.Prompts)+len(d.Contributes.Skills)+
		len(d.Contributes.MCPServers)+len(d.Contributes.Defaults) > 0
}

func validForgePermission(permission string) bool {
	switch permission {
	case "read", "comment", "review", "push", "open_pr":
		return true
	default:
		return false
	}
}

func validateFile(file File) error {
	clean := path.Clean(file.Path)
	if file.Path == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(file.Path) {
		return fmt.Errorf("path must be a relative package path")
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
	case ".go", ".yaegi", ".sh", ".bash", ".zsh", ".fish", ".ksh", ".csh",
		".py", ".pyw", ".rb", ".pl", ".pm", ".php", ".lua", ".tcl", ".r",
		".js", ".mjs", ".cjs", ".ts", ".bat", ".cmd", ".ps1":
		return true
	default:
		return false
	}
}
