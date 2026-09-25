package kit

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strings"

	"github.com/docker/sandbox-kit-spec/v3/spec"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/infrastructure/egress"
)

const (
	// DefaultHarnessUser is the Kit agent user: the harness runs as it when
	// the image names no user, or names root.
	DefaultHarnessUser = "1000"
	// WorkspaceDir is where a Kit container mounts the task worktree.
	WorkspaceDir = "/workspace"

	installHookUser = "0"
)

// hookBaseline is the platform baseline a hook sees besides its declared
// variables, taken from the image's environment, never the host's.
var hookBaseline = []string{"PATH", "HOME", "HOSTNAME", "TERM"}

var unsafeVolumeChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// ImageConfig is the workload image's runtime contract.
type ImageConfig struct {
	Entrypoint []string
	Cmd        []string
	Env        []string
	User       string
}

// LaunchParams are the run-specific inputs to a Kit container.
type LaunchParams struct {
	// Execution keys the Kit's volumes, so they survive retries of one
	// WorkflowExecution and are never shared with another.
	Execution  string
	ProxyToken string
	CAPath     string
	// Bound lists the credential services the run credential carries.
	Bound []string
}

// Hook is one lifecycle hook, ready to exec in the container.
type Hook struct {
	Argv       []string
	User       string
	Env        []string
	Background bool
}

// Volume is one volume@1 path backed by a named volume.
type Volume struct {
	Name string
	Path string
}

// Launch is everything archie needs to run a composed Kit for one run.
type Launch struct {
	Harness agentexec.HarnessSpec
	Install []Hook
	Startup []Hook
	Files   []spec.File
	Volumes []Volume
}

// Assemble derives a Kit container's launch from its admitted plan and the
// workload image's config. The harness never runs as root, and no hook sees
// a variable it did not declare beyond the image-derived baseline and the
// egress settings every sandbox process needs.
func Assemble(p *Plan, img ImageConfig, params LaunchParams) (Launch, error) {
	creds, err := spec.CredentialsOf(p.Capabilities)
	if err != nil {
		return Launch{}, err
	}
	lifecycle, err := spec.LifecycleOf(p.Capabilities)
	if err != nil {
		return Launch{}, err
	}
	volumes, err := spec.VolumesOf(p.Capabilities)
	if err != nil {
		return Launch{}, err
	}

	proxyEnv := egress.ProxyEnv(params.ProxyToken, params.CAPath)
	runtimeVars := map[string]string{"WORKSPACE_DIR": WorkspaceDir}
	for _, c := range creds {
		runtimeVars[credentialModeVar(c.Service)] = credentialMode(c, slices.Contains(params.Bound, c.Service))
	}

	l := Launch{Harness: agentexec.HarnessSpec{
		User:   harnessUser(img.User),
		Launch: slices.Concat(img.Entrypoint, img.Cmd),
		Env:    slices.Concat(img.Env, []string{"WORKSPACE_DIR=" + WorkspaceDir}, proxyEnv, egress.SentinelEnv(creds)),
	}}
	if s := p.Sessions; s != nil {
		l.Harness.Prompt, l.Harness.Resume, l.Harness.Continue = s.Prompt, s.Resume, s.Continue
	}

	baseline := slices.Concat(pick(img.Env, hookBaseline), proxyEnv)
	if lifecycle != nil {
		for _, h := range lifecycle.Install {
			l.Install = append(l.Install, Hook{
				Argv: slices.Clone(h.Command), User: orDefault(h.User, installHookUser),
				Env: slices.Concat(baseline, declared(h.Env, runtimeVars)),
			})
		}
		for _, h := range lifecycle.Startup {
			l.Startup = append(l.Startup, Hook{
				Argv: slices.Clone(h.Command), User: orDefault(h.User, DefaultHarnessUser),
				Env: slices.Concat(baseline, declared(h.Env, runtimeVars)), Background: h.Background,
			})
		}
		l.Files = lifecycle.Files
	}
	for _, v := range volumes {
		l.Volumes = append(l.Volumes, Volume{Name: volumeName(params.Execution, v.Path), Path: v.Path})
	}
	return l, nil
}

func harnessUser(imageUser string) string {
	name, _, _ := strings.Cut(imageUser, ":")
	if name == "" || name == "root" || name == "0" {
		return DefaultHarnessUser
	}
	return imageUser
}

func credentialModeVar(service string) string {
	return "SBX_CRED_" + strings.ToUpper(strings.ReplaceAll(service, "-", "_")) + "_MODE"
}

func credentialMode(c spec.CredentialCapability, bound bool) string {
	switch {
	case !bound:
		return "none"
	case c.APIKey != nil:
		return "apikey"
	default:
		return "oauth"
	}
}

// declared resolves a hook's declared variable names against the runtime
// variables archie provides; a name archie does not provide stays unset.
func declared(names []string, vars map[string]string) []string {
	var env []string
	for _, name := range names {
		if v, ok := vars[name]; ok {
			env = append(env, name+"="+v)
		}
	}
	return env
}

// pick returns the entries of env whose names are listed.
func pick(env, names []string) []string {
	var out []string
	for _, e := range env {
		if name, _, _ := strings.Cut(e, "="); slices.Contains(names, name) {
			out = append(out, e)
		}
	}
	return out
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func volumeName(execution, path string) string {
	sum := sha256.Sum256([]byte(path))
	return "archie-kit-" + unsafeVolumeChars.ReplaceAllString(execution, "_") + "-" + hex.EncodeToString(sum[:6])
}
