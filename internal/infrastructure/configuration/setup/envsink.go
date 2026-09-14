package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// EnvFileSink is a SecretSink that stores secrets in the env file beside
// config.toml (${XDG_CONFIG_HOME:-~/.config}/archie/env by default). The
// daemon reads that file through systemd's EnvironmentFile, so the line
// format must match what install.sh's set_env_key has always written -- a
// single-quoted KEY='value' line, with an embedded single quote escaped as a
// backslash between two single quotes -- rather than any other
// plausible-looking format.
//
// Put buffers; nothing is written until Commit, and Commit rewrites the file
// atomically with mode 0600. The caller must only Commit once the config
// text referencing these secrets has been proven loadable, so a validation
// failure leaves neither a config nor a secret on disk.
type EnvFileSink struct {
	path    string
	entries map[string]string
}

// NewEnvFileSink returns a sink that writes the env file at path.
func NewEnvFileSink(path string) *EnvFileSink {
	return &EnvFileSink{path: path, entries: map[string]string{}}
}

// Put records an env-file entry. Only the "env" engine has a representation
// in the env file; every key setup writes uses engine "env".
func (s *EnvFileSink) Put(engine, key, value string) error {
	if engine != "env" {
		return fmt.Errorf("setup: env file sink cannot store engine %q secrets", engine)
	}
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("setup: env file sink: empty key")
	}
	s.entries[key] = value
	return nil
}

// Commit writes every Put entry, replacing any existing line for the same
// key, leaving unrelated lines byte-identical, and persists the result
// atomically.
func (s *EnvFileSink) Commit() error {
	if len(s.entries) == 0 {
		return nil
	}
	keys := make([]string, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []string
	existing, err := readEnvLines(s.path)
	if err != nil {
		return fmt.Errorf("setup: read env file: %w", err)
	}
	for _, line := range existing {
		keep := true
		for _, key := range keys {
			if strings.HasPrefix(line, key+"=") {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, line)
		}
	}
	for _, key := range keys {
		out = append(out, key+"='"+escapeEnvValue(s.entries[key])+"'")
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("setup: create env directory: %w", err)
	}
	if err := writeEnvFileAtomic(s.path, []byte(strings.Join(out, "\n")+"\n")); err != nil {
		return fmt.Errorf("setup: write env file: %w", err)
	}
	return nil
}

// readEnvLines reads the env file's lines, without their trailing newline.
// A missing or empty file yields no lines.
func readEnvLines(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	text := strings.TrimSuffix(string(b), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

// escapeEnvValue renders value as the contents of a single-quoted
// systemd EnvironmentFile value: a literal single quote becomes a backslash
// between two single quotes. This is byte-for-byte what install.sh's
// set_env_key produces, so the Go sink and the shell helper cannot disagree
// about the on-disk format.
func escapeEnvValue(v string) string {
	return strings.ReplaceAll(v, "'", `'\''`)
}

// writeEnvFileAtomic writes data to path via a same-directory temp file and
// rename, mirroring set_env_key's mktemp-then-mv so a reader never observes
// a half-written env file.
func writeEnvFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".env.XXXXXX")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
