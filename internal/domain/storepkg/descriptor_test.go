package storepkg

import (
	"strings"
	"testing"
)

const validDescriptor = `apiVersion: dev.archie.package.v1
displayName: Review workflow
description: Reviews pull requests
version: 1.2.3
contributes:
  workflows: [review]
  prompts: [reviewer]
  skills: [review-skill]
  mcpServers: [forge]
  defaults: [review-profile]
requires:
  - name: review-kit
    digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
authority:
  credentialServices: [github]
  egressHosts: [api.github.com]
  forgePermissions: [read, comment, review, push, open_pr]
  triggers: [watched_repository_events, schedules]
  tools: [archie.search]
files:
  - path: workflows/review.yaml
    mode: 0644
`

func TestDecodeDescriptor(t *testing.T) {
	got, err := Decode(strings.NewReader(validDescriptor))
	if err != nil {
		t.Fatal(err)
	}
	if got.APIVersion != "dev.archie.package.v1" || got.DisplayName != "Review workflow" || got.Version != "1.2.3" {
		t.Fatalf("descriptor metadata = %#v", got)
	}
	if len(got.Contributes.Workflows) != 1 || len(got.Contributes.MCPServers) != 1 || len(got.Requires) != 1 {
		t.Fatalf("descriptor contributions/requirements = %#v", got)
	}
	if len(got.Authority.ForgePermissions) != 5 || got.Files[0].Path != "workflows/review.yaml" {
		t.Fatalf("descriptor authority/files = %#v", got)
	}
}

func TestDecodeDescriptorRejectsUnknownFields(t *testing.T) {
	input := strings.Replace(validDescriptor, "displayName: Review workflow", "displayName: Review workflow\nunknownField: surprise", 1)
	if _, err := Decode(strings.NewReader(input)); err == nil {
		t.Fatal("Decode accepted unknown field")
	}
}

func TestDescriptorValidate(t *testing.T) {
	cases := []struct {
		name    string
		edit    func(*Descriptor)
		wantErr string
	}{
		{name: "valid", edit: func(*Descriptor) {}},
		{name: "missing display name", edit: func(d *Descriptor) { d.DisplayName = "" }, wantErr: "display name"},
		{name: "unsupported api version", edit: func(d *Descriptor) { d.APIVersion = "dev.archie.package.v2" }, wantErr: "apiVersion"},
		{name: "unpinned kit", edit: func(d *Descriptor) { d.Requires = []PackageRef{{Name: "kit", Digest: ""}} }, wantErr: "digest"},
		{name: "tag kit", edit: func(d *Descriptor) { d.Requires = []PackageRef{{Name: "kit", Digest: "latest"}} }, wantErr: "digest"},
		{
			name: "go source", edit: func(d *Descriptor) {
				d.Files = []File{{Path: "plugin/main.go", Mode: 0o644}}
			}, wantErr: "declarative",
		},
		{
			name: "yaegi source", edit: func(d *Descriptor) {
				d.Files = []File{{Path: "plugin/main.yaegi", Mode: 0o644}}
			}, wantErr: "declarative",
		},
		{
			name: "script", edit: func(d *Descriptor) {
				d.Files = []File{{Path: "hooks/setup.sh", Mode: 0o644}}
			}, wantErr: "declarative",
		},
		{
			name: "executable mode", edit: func(d *Descriptor) {
				d.Files = []File{{Path: "workflows/review.yaml", Mode: 0o755}}
			}, wantErr: "executable",
		},
		{
			name: "full executable mode", edit: func(d *Descriptor) {
				d.Files = []File{{Path: "workflows/review.yaml", Mode: 0o100755}}
			}, wantErr: "executable",
		},
		{name: "invalid forge permission", edit: func(d *Descriptor) { d.Authority.ForgePermissions = []string{"admin"} }, wantErr: "forge permission"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := Decode(strings.NewReader(validDescriptor))
			if err != nil {
				t.Fatal(err)
			}
			tc.edit(&d)
			err = d.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantErr)) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestDecodeRejectsMalformedAndMultipleDocuments(t *testing.T) {
	for _, input := range []string{"displayName: [", validDescriptor + "---\n" + validDescriptor} {
		if _, err := Decode(strings.NewReader(input)); err == nil {
			t.Fatalf("Decode accepted invalid input %q", input)
		}
	}
}
