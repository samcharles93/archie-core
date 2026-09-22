package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/identity"
)

var testActingIdentity = identity.Identity{
	ID:          "40000000-0000-5000-8000-000000000001",
	Kind:        identity.KindUser,
	DisplayName: "sam",
	Lifecycle:   identity.LifecycleActive,
	Version:     1,
}

// withIdentityCheck builds the middleware over a stub provider, which is the
// seam the dashboard sees: verification and binding are proved in
// internal/infrastructure/oidc and internal/domain/identity, and what is proved
// here is that a request either resolves or is refused.
func withIdentityCheck(t *testing.T, authenticate func(context.Context, string) (identity.Identity, error)) http.Handler {
	t.Helper()
	s := &Server{Authenticate: authenticate}
	return s.requireToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value, ok := ActingIdentity(r.Context())
		if !ok {
			t.Error("handler ran without a resolved identity")
			return
		}
		_, _ = w.Write([]byte(string(value.ID)))
	}))
}

func TestIdentityRequestResolvesToTheCredentialSubject(t *testing.T) {
	seen := ""
	handler := withIdentityCheck(t, func(_ context.Context, raw string) (identity.Identity, error) {
		seen = raw
		return testActingIdentity, nil
	})

	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/tasks/7/action", strings.NewReader(`{"actor":"someone-else"}`))
	request.Header.Set("Authorization", "Bearer "+"a-provider-token")
	// A caller-supplied identity must not be consulted: the only source of the
	// acting identity is the credential the provider signed.
	request.Header.Set("X-Archie-Identity", "attacker-chosen")
	request.Header.Set("X-Archie-Actor", "attacker-chosen")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if seen != "a-provider-token" {
		t.Fatalf("verifier saw %q, want the presented credential", seen)
	}
	if got := recorder.Body.String(); got != string(testActingIdentity.ID) {
		t.Fatalf("resolved identity = %q, want %q", got, testActingIdentity.ID)
	}
}

func TestIdentityRequestRefusesEveryCallerItCannotAttribute(t *testing.T) {
	tests := []struct {
		name       string
		credential string
		authErr    error
		wantStatus int
	}{
		{
			name:       "no credential presented",
			credential: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "credential rejected",
			credential: "Bearer expired-or-foreign",
			authErr:    identity.ErrCredentialRejected,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "subject not bound to an identity",
			credential: "Bearer valid-but-unknown",
			authErr:    identity.ErrSubjectUnbound,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "identity may not act",
			credential: "Bearer valid-but-suspended",
			authErr:    identity.ErrIdentityInactive,
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := withIdentityCheck(t, func(_ context.Context, _ string) (identity.Identity, error) {
				if tc.authErr == nil {
					t.Fatal("the verifier was called with no credential to verify")
				}
				return identity.Identity{}, tc.authErr
			})

			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/tasks", nil)
			if tc.credential != "" {
				request.Header.Set("Authorization", tc.credential)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusUnauthorized &&
				recorder.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("a 401 must name the scheme it accepts")
			}
		})
	}
}

// TestIdentityCheckReplacesTheSharedToken: with a provider configured the shared
// token is no longer a way in, because a credential that names nobody cannot
// attribute what it does.
func TestIdentityCheckReplacesTheSharedToken(t *testing.T) {
	handler := withIdentityCheck(t, func(_ context.Context, raw string) (identity.Identity, error) {
		if raw == "" {
			return identity.Identity{}, identity.ErrNoCredential
		}
		return testActingIdentity, nil
	})

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: tokenCookie, Value: "the-shared-token"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

// TestSharedTokenStillGatesAnInstanceWithNoProvider: with no provider configured
// the shared token remains the gate, which is the loopback-friendly behaviour a
// single-operator instance relies on.
func TestSharedTokenStillGatesAnInstanceWithNoProvider(t *testing.T) {
	s := &Server{Token: ""}
	reached := false
	handler := s.requireToken(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if !reached {
		t.Fatal("a token-less loopback server refused a request")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

// TestRefusedBrowserIsSentToSignIn: with a sign-in flow configured, a browser that
// cannot authenticate belongs at the provider, not on a shared-token paste page
// that cannot help it. A refusal signing in again cannot fix -- a suspended
// identity -- is answered instead of redirected.
func TestRefusedBrowserIsSentToSignIn(t *testing.T) {
	tests := []struct {
		name       string
		authErr    error
		login      identity.LoginFlow
		wantStatus int
		wantLogin  bool
	}{
		{
			name:       "no credential, sign-in available",
			authErr:    identity.ErrCredentialRejected,
			login:      stubFlow{},
			wantStatus: http.StatusSeeOther,
			wantLogin:  true,
		},
		{
			name:       "no credential, no sign-in flow",
			authErr:    identity.ErrCredentialRejected,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "suspended identity is told, not redirected",
			authErr:    identity.ErrIdentityInactive,
			login:      stubFlow{},
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{
				Login: tc.login,
				Authenticate: func(context.Context, string) (identity.Identity, error) {
					return identity.Identity{}, tc.authErr
				},
			}
			handler := s.requireToken(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("an unauthenticated request reached the handler")
			}))

			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			request.Header.Set("Authorization", "Bearer a-token")
			request.Header.Set("Accept", "text/html")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.wantStatus)
			}
			location := recorder.Header().Get("Location")
			if tc.wantLogin && location != loginPath {
				t.Fatalf("Location = %q, want %q", location, loginPath)
			}
			if !tc.wantLogin && location != "" {
				t.Fatalf("a refusal that signing in cannot fix redirected to %q", location)
			}
		})
	}
}
