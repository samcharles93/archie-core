package storepkg

import (
	"strings"
	"testing"
)

func extensionDescriptor(mutate func(*Descriptor)) Descriptor {
	d := Descriptor{
		APIVersion: APIVersion, DisplayName: "bws", Version: "1.0.0",
		Contributes: Contributions{Extensions: []Extension{{Surface: SurfaceSecretEngine, Path: "bin/bws"}}},
		Files:       []File{{Path: "bin/bws", Mode: 0o755}},
	}
	mutate(&d)
	return d
}

// Only a file a package declares as an extension may be executable; every other
// file stays declarative.
func TestDescriptorExecutableOnlyForExtensions(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Descriptor)
		wantErr string
	}{
		{name: "a declared extension may be executable", mutate: func(*Descriptor) {}},
		{name: "an executable file that is not an extension is refused", mutate: func(d *Descriptor) {
			d.Files = append(d.Files, File{Path: "helper", Mode: 0o755})
		}, wantErr: "executable file modes"},
		{name: "host code beside an extension is refused", mutate: func(d *Descriptor) {
			d.Files = append(d.Files, File{Path: "run.sh", Mode: 0o644})
		}, wantErr: "host code"},
		{name: "an extension must be owner-executable", mutate: func(d *Descriptor) {
			d.Files[0].Mode = 0o644
		}, wantErr: "owner-executable"},
		{name: "an extension must be a declared file", mutate: func(d *Descriptor) {
			d.Contributes.Extensions[0].Path = "bin/other"
		}, wantErr: "not a declared package file"},
		{name: "an unknown surface is refused", mutate: func(d *Descriptor) {
			d.Contributes.Extensions[0].Surface = "issue-tracker"
		}, wantErr: "unknown surface"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := extensionDescriptor(tt.mutate).Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// An accepted record is an upper bound: an extension may read only the env vars
// the operator accepted.
func TestAuthorityEnvIsBounded(t *testing.T) {
	accepted := Authority{Env: []string{"BWS_ACCESS_TOKEN"}}
	if !accepted.Covers(Authority{Env: []string{"BWS_ACCESS_TOKEN"}}) {
		t.Fatal("an accepted env var must be covered")
	}
	if accepted.Covers(Authority{Env: []string{"BWS_ACCESS_TOKEN", "HOME_SECRET"}}) {
		t.Fatal("an env var beyond the accepted record must not be covered")
	}
}
