package enginehost

import (
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name    string
		cmd     string
		args    []string
		want    string
		wantErr bool
	}{
		{
			name: "trims stdout",
			cmd:  "echo",
			args: []string{"  hello world  "},
			want: "hello world",
		},
		{
			name: "multi-word args",
			cmd:  "printf",
			args: []string{"%s-%s", "a", "b"},
			want: "a-b",
		},
		{
			name:    "nonexistent binary",
			cmd:     "definitely-not-a-real-binary-archie-core-test",
			args:    nil,
			wantErr: true,
		},
		{
			name:    "non-zero exit",
			cmd:     "false",
			args:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Run(tt.cmd, tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Run(%q, %v) error = nil, want error", tt.cmd, tt.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("Run(%q, %v) error = %v, want nil", tt.cmd, tt.args, err)
			}
			if got != tt.want {
				t.Fatalf("Run(%q, %v) = %q, want %q", tt.cmd, tt.args, got, tt.want)
			}
		})
	}
}

func TestRunErrorWrapsCommandName(t *testing.T) {
	_, err := Run("false", nil)
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "run false") {
		t.Fatalf("Run() error = %v, want it to mention the command name", err)
	}
}

func TestSymbolsExposesRun(t *testing.T) {
	pkg, ok := Symbols["github.com/samcharles93/archie-core/internal/secret/enginehost/enginehost"]
	if !ok {
		t.Fatal("Symbols missing the enginehost package entry")
	}
	fn, ok := pkg["Run"]
	if !ok {
		t.Fatal("Symbols missing Run")
	}
	if fn.Kind().String() != "func" {
		t.Fatalf("Symbols[\"Run\"].Kind() = %v, want func", fn.Kind())
	}
}
