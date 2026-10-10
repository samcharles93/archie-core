package worktree

import (
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v6"
	gitconfig "github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
)

func TestForgeHTTPAuthentication(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bare, err := git.PlainInit(filepath.Join(root, "org", "repo.git"), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := bare.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))); err != nil {
		t.Fatal(err)
	}
	cfg, err := bare.Config()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Raw.Section("http").SetOption("receivepack", "true")
	if err := bare.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	seedDir := t.TempDir()
	seed, err := git.PlainInit(seedDir, false)
	if err != nil {
		t.Fatal(err)
	}
	seedConfig, err := seed.Config()
	if err != nil {
		t.Fatal(err)
	}
	seedConfig.Raw.Section("commit").SetOption("gpgsign", "false")
	if err := seed.SetConfig(seedConfig); err != nil {
		t.Fatal(err)
	}
	if err := seed.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "seed.txt"), []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{BotUser: "archie", BotEmail: "archie@example.test", Token: "secret-for-origin"}
	if _, err := manager.CommitAll(t.Context(), seedDir, "seed"); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{filepath.Join(root, "org", "repo.git")}}); err != nil {
		t.Fatal(err)
	}
	if err := seed.PushContext(t.Context(), &git.PushOptions{}); err != nil {
		t.Fatal(err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	backend := &cgi.Handler{Path: gitPath, Args: []string{"http-backend"}, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}}
	for i, tc := range []struct {
		name                    string
		redirect, foreign, dumb bool
	}{
		{name: "direct"}, {name: "same origin", redirect: true}, {name: "cross origin", foreign: true}, {name: "dumb HTTP", dumb: true},
	} {
		// One issue per transport: subtests share the bare repo, and a shared
		// branch would make the second push a non-fast-forward (or, when two
		// commits land in the same second, an identical-hash no-op).
		issue := i + 1
		t.Run(tc.name, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("forge credential reached another origin")
				}
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.foreign {
					http.Redirect(w, r, target.URL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
					return
				}
				user, password, ok := r.BasicAuth()
				if !ok || user != "archie" || password != manager.Token {
					http.Error(w, "authentication required", http.StatusUnauthorized)
					return
				}
				if tc.dumb {
					w.Header().Set("Content-Type", "text/plain")
					_, _ = w.Write([]byte("not a smart Git endpoint"))
					return
				}
				if tc.redirect && !strings.HasPrefix(r.URL.Path, "/redirect/") {
					http.Redirect(w, r, "/redirect"+r.URL.RequestURI(), http.StatusTemporaryRedirect)
					return
				}
				r.URL.Path = strings.TrimPrefix(r.URL.Path, "/redirect")
				backend.ServeHTTP(w, r)
			}))
			defer server.Close()
			m := *manager
			m.BaseURL = server.URL
			m.WorkDir = t.TempDir()
			dir, branch, err := m.Prepare(t.Context(), "org", "repo", "main", issue, "test", "", "", Fresh)
			if tc.foreign || tc.dumb {
				if err == nil {
					t.Fatal("unsupported or unauthenticated endpoint succeeded")
				}
				expected := "origin boundary"
				if tc.dumb {
					expected = "dumb HTTP protocol is not supported"
				}
				if !strings.Contains(err.Error(), expected) || strings.Contains(err.Error(), m.Token) {
					t.Fatalf("not actionable or leaked token: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "change.txt"), []byte("change"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := m.CommitAll(t.Context(), dir, "change"); err != nil {
				t.Fatal(err)
			}
			if err := m.Push(t.Context(), dir, branch); err != nil {
				t.Fatal(err)
			}
			if _, err := bare.Reference(plumbing.NewBranchReferenceName(branch), true); err != nil {
				t.Fatalf("pushed branch missing: %v", err)
			}
			if _, _, err := m.Prepare(t.Context(), "org", "repo", "main", issue, "test", "", "", Target(branch)); err != nil {
				t.Fatalf("authenticated fetch: %v", err)
			}
		})
	}
}
