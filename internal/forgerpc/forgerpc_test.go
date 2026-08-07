package forgerpc

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natssrv "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/forge"
)

func startEmbedded(t *testing.T) *server.Server {
	t.Helper()
	srv := natssrv.RunRandClientPortServer()
	t.Cleanup(srv.Shutdown)
	return srv
}

func connect(t *testing.T, url string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// fakeForge implements forge.Forge, recording calls to the four methods
// workflow stages invoke mid-run. Every other method panics if called  --
// this RPC layer must never proxy them.
type fakeForge struct {
	comments    []commentCall
	closes      []closeCall
	prs         []prCall
	stateLabels []stateLabelCall
	branches    []branchCall

	commentErr error
	closeErr   error
	prErr      error
}

type commentCall struct {
	owner, repo string
	number      int
	body        string
}
type closeCall struct {
	owner, repo string
	number      int
	comment     string
}
type prCall struct {
	owner, repo, title, head, base, body string
}
type stateLabelCall struct {
	owner, repo string
	number      int
	label       string
	knownLabels []string
}
type branchCall struct {
	owner, repo, branch string
	number              int
}

func (f *fakeForge) Comment(_ context.Context, owner, repo string, number int, body string) (int64, error) {
	f.comments = append(f.comments, commentCall{owner, repo, number, body})
	if f.commentErr != nil {
		return 0, f.commentErr
	}
	return 99, nil
}

func (f *fakeForge) CloseIssue(_ context.Context, owner, repo string, number int, comment string) error {
	f.closes = append(f.closes, closeCall{owner, repo, number, comment})
	return f.closeErr
}

func (f *fakeForge) CreatePR(_ context.Context, owner, repo, title, head, base, body string) (int, error) {
	f.prs = append(f.prs, prCall{owner, repo, title, head, base, body})
	if f.prErr != nil {
		return 0, f.prErr
	}
	return 7, nil
}

func (f *fakeForge) SetStateLabel(_ context.Context, owner, repo string, number int, label string, knownLabels []string) {
	f.stateLabels = append(f.stateLabels, stateLabelCall{owner, repo, number, label, knownLabels})
}

func (f *fakeForge) LinkBranch(_ context.Context, owner, repo string, number int, branch string) error {
	f.branches = append(f.branches, branchCall{owner: owner, repo: repo, number: number, branch: branch})
	return nil
}

func (f *fakeForge) AcceptInvitations(context.Context) error { panic("unexpected call") }
func (f *fakeForge) AssignedIssues(context.Context, string, string, string) ([]forge.Issue, error) {
	panic("unexpected call")
}

func (f *fakeForge) IssuesWithLabel(context.Context, string, string, string) ([]forge.Issue, error) {
	panic("unexpected call")
}

func (f *fakeForge) RepliesAfter(context.Context, string, string, int, int64, string) ([]forge.Reply, error) {
	panic("unexpected call")
}

func (f *fakeForge) PRState(context.Context, string, string, int) (string, error) {
	panic("unexpected call")
}

func (f *fakeForge) CreateIssue(context.Context, string, string, string, string, []string) (int, error) {
	panic("unexpected call")
}

func (f *fakeForge) React(context.Context, string, string, int, string) error {
	panic("unexpected call")
}
func (f *fakeForge) VerifyPush(context.Context, string, string) error { panic("unexpected call") }

func newTestServer(t *testing.T) (*fakeForge, *Client) {
	t.Helper()
	fg := &fakeForge{}
	srv := startEmbedded(t)
	url := srv.ClientURL()

	serverConn := connect(t, url)
	rpcServer := &Server{Forge: fg, Log: slog.New(slog.DiscardHandler)}
	unsub, err := rpcServer.Register(serverConn)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(unsub)

	clientConn := connect(t, url)
	client := &Client{Conn: clientConn, Timeout: 2 * time.Second}
	return fg, client
}

func TestClientCommentPersistsViaServer(t *testing.T) {
	fg, client := newTestServer(t)

	id, err := client.Comment(context.Background(), "acme", "widget", 1, "hello")
	if err != nil {
		t.Fatalf("Comment: %v", err)
	}
	if id != 99 {
		t.Fatalf("expected comment id 99, got %d", id)
	}
	if len(fg.comments) != 1 || fg.comments[0] != (commentCall{"acme", "widget", 1, "hello"}) {
		t.Fatalf("comment did not reach server, got %+v", fg.comments)
	}
}

func TestClientCloseIssuePersistsViaServer(t *testing.T) {
	fg, client := newTestServer(t)

	if err := client.CloseIssue(context.Background(), "acme", "widget", 2, "done"); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
	if len(fg.closes) != 1 || fg.closes[0] != (closeCall{"acme", "widget", 2, "done"}) {
		t.Fatalf("close did not reach server, got %+v", fg.closes)
	}
}

func TestClientCreatePRPersistsViaServer(t *testing.T) {
	fg, client := newTestServer(t)

	num, err := client.CreatePR(context.Background(), "acme", "widget", "title", "head", "main", "body")
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if num != 7 {
		t.Fatalf("expected PR number 7, got %d", num)
	}
	if len(fg.prs) != 1 || fg.prs[0] != (prCall{"acme", "widget", "title", "head", "main", "body"}) {
		t.Fatalf("PR did not reach server, got %+v", fg.prs)
	}
}

func TestClientLinkBranchPersistsViaServer(t *testing.T) {
	f, client := newTestServer(t)

	if err := client.LinkBranch(context.Background(), "acme", "widget", 4, "fix/4-bug"); err != nil {
		t.Fatalf("LinkBranch: %v", err)
	}
	if len(f.branches) != 1 || f.branches[0] != (branchCall{owner: "acme", repo: "widget", number: 4, branch: "fix/4-bug"}) {
		t.Fatalf("branch link did not reach server, got %+v", f.branches)
	}
}

func TestClientPropagatesServerError(t *testing.T) {
	fg := &fakeForge{commentErr: errors.New("forge unavailable")}
	srv := startEmbedded(t)
	url := srv.ClientURL()

	rpcServer := &Server{Forge: fg, Log: slog.New(slog.DiscardHandler)}
	unsub, err := rpcServer.Register(connect(t, url))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(unsub)

	client := &Client{Conn: connect(t, url), Timeout: 2 * time.Second}
	_, err = client.Comment(context.Background(), "acme", "widget", 1, "hello")
	if err == nil {
		t.Fatal("expected Comment to propagate the server-side error")
	}
}

func TestClientCommentTimesOutWithNoResponder(t *testing.T) {
	srv := startEmbedded(t)
	client := &Client{Conn: connect(t, srv.ClientURL()), Timeout: 100 * time.Millisecond}

	_, err := client.Comment(context.Background(), "acme", "widget", 1, "hello")
	if err == nil {
		t.Fatal("expected Comment to time out with no server registered")
	}
}

// TestIdentityScopedRoutingIsolatesForges proves the critical fix: an
// identity-scoped client must reach the forge wired to that identity's
// subject, never the root forge. Before identity-scoped subjects existed,
// every container-mode task's RPC was served by the root forge regardless
// of which identity owned the task.
func TestIdentityScopedRoutingIsolatesForges(t *testing.T) {
	rootFg := &fakeForge{}
	archieFg := &fakeForge{}
	srv := startEmbedded(t)
	url := srv.ClientURL()

	serverConn := connect(t, url)
	// Root server on root subjects.
	unsubRoot, err := (&Server{Forge: rootFg, Log: slog.New(slog.DiscardHandler)}).Register(serverConn)
	if err != nil {
		t.Fatalf("register root: %v", err)
	}
	t.Cleanup(unsubRoot)
	// Identity server on archie-scoped subjects.
	unsubArch, err := (&Server{Forge: archieFg, Log: slog.New(slog.DiscardHandler)}).RegisterFor(serverConn, "archie")
	if err != nil {
		t.Fatalf("register archie: %v", err)
	}
	t.Cleanup(unsubArch)

	clientConn := connect(t, url)
	ctx := context.Background()

	// Identity-scoped call reaches archie's forge only.
	archieClient := &Client{Conn: clientConn, Timeout: 2 * time.Second, Identity: "archie"}
	if err := archieClient.CloseIssue(ctx, "acme", "widget", 1, "done"); err != nil {
		t.Fatalf("identity-scoped CloseIssue: %v", err)
	}
	if len(archieFg.closes) != 1 {
		t.Fatalf("archie forge closes = %d, want 1", len(archieFg.closes))
	}
	if len(rootFg.closes) != 0 {
		t.Fatalf("root forge closes = %d, want 0 (identity call leaked to root)", len(rootFg.closes))
	}

	// Root (identity-less) call reaches the root forge only.
	rootClient := &Client{Conn: clientConn, Timeout: 2 * time.Second}
	if err := rootClient.CloseIssue(ctx, "acme", "widget", 2, "done"); err != nil {
		t.Fatalf("root-scoped CloseIssue: %v", err)
	}
	if len(rootFg.closes) != 1 {
		t.Fatalf("root forge closes = %d, want 1", len(rootFg.closes))
	}
	if len(archieFg.closes) != 1 {
		t.Fatalf("archie forge closes = %d, want still 1 (root call leaked to identity)", len(archieFg.closes))
	}
}

// SubjectFor leaves the root subject unchanged for an empty identity and
// prefixes the identity for a non-empty one -- the root subjects must stay
// byte-identical so single-identity deployments and older agent images keep
// working.
func TestSubjectFor(t *testing.T) {
	if got := SubjectFor("", SubjectComment); got != SubjectComment {
		t.Fatalf("SubjectFor(\"\", %q) = %q, want unchanged root subject", SubjectComment, got)
	}
	if got := SubjectFor("archie", SubjectComment); got != "archie.forge.archie.comment" {
		t.Fatalf("SubjectFor(\"archie\", %q) = %q", SubjectComment, got)
	}
	if got := SubjectFor("archie", SubjectCreatePR); got != "archie.forge.archie.create_pr" {
		t.Fatalf("SubjectFor(\"archie\", %q) = %q", SubjectCreatePR, got)
	}
}
