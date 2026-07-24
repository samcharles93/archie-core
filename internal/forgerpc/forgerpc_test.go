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
// workflow stages invoke mid-run. Every other method panics if called —
// this RPC layer must never proxy them.
type fakeForge struct {
	comments    []commentCall
	closes      []closeCall
	prs         []prCall
	stateLabels []stateLabelCall

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

	id, err := client.Comment(context.Background(), "sam", "archie", 1, "hello")
	if err != nil {
		t.Fatalf("Comment: %v", err)
	}
	if id != 99 {
		t.Fatalf("expected comment id 99, got %d", id)
	}
	if len(fg.comments) != 1 || fg.comments[0] != (commentCall{"sam", "archie", 1, "hello"}) {
		t.Fatalf("comment did not reach server, got %+v", fg.comments)
	}
}

func TestClientCloseIssuePersistsViaServer(t *testing.T) {
	fg, client := newTestServer(t)

	if err := client.CloseIssue(context.Background(), "sam", "archie", 2, "done"); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
	if len(fg.closes) != 1 || fg.closes[0] != (closeCall{"sam", "archie", 2, "done"}) {
		t.Fatalf("close did not reach server, got %+v", fg.closes)
	}
}

func TestClientCreatePRPersistsViaServer(t *testing.T) {
	fg, client := newTestServer(t)

	num, err := client.CreatePR(context.Background(), "sam", "archie", "title", "head", "main", "body")
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if num != 7 {
		t.Fatalf("expected PR number 7, got %d", num)
	}
	if len(fg.prs) != 1 || fg.prs[0] != (prCall{"sam", "archie", "title", "head", "main", "body"}) {
		t.Fatalf("PR did not reach server, got %+v", fg.prs)
	}
}

func TestClientSetStateLabelPersistsViaServer(t *testing.T) {
	fg, client := newTestServer(t)

	client.SetStateLabel(context.Background(), "sam", "archie", 3, "archie:working", []string{"archie:queued", "archie:working"})

	if len(fg.stateLabels) != 1 {
		t.Fatalf("expected 1 state label call, got %d", len(fg.stateLabels))
	}
	got := fg.stateLabels[0]
	if got.owner != "sam" || got.repo != "archie" || got.number != 3 || got.label != "archie:working" {
		t.Fatalf("state label call mismatch: %+v", got)
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
	_, err = client.Comment(context.Background(), "sam", "archie", 1, "hello")
	if err == nil {
		t.Fatal("expected Comment to propagate the server-side error")
	}
}

func TestClientCommentTimesOutWithNoResponder(t *testing.T) {
	srv := startEmbedded(t)
	client := &Client{Conn: connect(t, srv.ClientURL()), Timeout: 100 * time.Millisecond}

	_, err := client.Comment(context.Background(), "sam", "archie", 1, "hello")
	if err == nil {
		t.Fatal("expected Comment to time out with no server registered")
	}
}
