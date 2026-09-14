package setup

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
)

func TestParseSecretRef(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    config.SecretRef
		wantErr bool
	}{
		{name: "env engine", in: "env:ARCHIE_GITHUB_TOKEN", want: config.SecretRef{Engine: "env", Key: "ARCHIE_GITHUB_TOKEN"}},
		{name: "bws engine", in: "bws:OPENAI_API_KEY", want: config.SecretRef{Engine: "bws", Key: "OPENAI_API_KEY"}},
		{name: "operator-supplied engine", in: "vault:TELEGRAM_BOT_TOKEN", want: config.SecretRef{Engine: "vault", Key: "TELEGRAM_BOT_TOKEN"}},
		{name: "hyphenated engine", in: "my-engine:KEY", want: config.SecretRef{Engine: "my-engine", Key: "KEY"}},
		{name: "surrounding space is trimmed", in: "  env : ARCHIE_TOKEN  ", want: config.SecretRef{Engine: "env", Key: "ARCHIE_TOKEN"}},
		{name: "no colon", in: "ARCHIE_GITHUB_TOKEN", wantErr: true},
		{name: "empty engine", in: ":ARCHIE_TOKEN", wantErr: true},
		{name: "empty key", in: "env:", wantErr: true},
		{name: "engine with a space", in: "my engine:KEY", wantErr: true},
		// Engine names are Yaegi plugin filenames and the shipped ones are bare
		// identifiers (age, sops, vault), so a path separator is never valid.
		{name: "engine with a slash", in: "vault/openbao:KEY", wantErr: true},
		{name: "key with a colon", in: "env:A:B", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSecretRef(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseSecretRef(%q) = %+v, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSecretRef(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseSecretRef(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}
