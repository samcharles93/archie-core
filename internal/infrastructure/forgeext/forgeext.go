// Package forgeext adapts a forge extension, served over the forge.v1 gRPC
// surface, to the host's forge.Forge. One extension process serves one
// configured forge instance.
package forgeext

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	forgev1 "github.com/samcharles93/archie-core/internal/contracts/forge/v1"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/infrastructure/extension"
)

// Surface is the go-plugin surface name a forge binary serves.
const Surface = "forge"

// Plugin is the go-plugin shim for the surface. Impl is set only by a forge
// binary serving it.
type Plugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl forgev1.ForgeServiceServer
}

func (p *Plugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	forgev1.RegisterForgeServiceServer(s, p.Impl)
	return nil
}

func (*Plugin) GRPCClient(_ context.Context, _ *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return forgev1.NewForgeServiceClient(c), nil
}

// Instance is one forge to run: the plugin binary and what Configure hands it.
type Instance struct {
	Spec     extension.Spec
	Host     string
	Token    string
	Settings map[string]string
}

// Forge calls a forge extension. It owns its process: a call that finds the
// process dead starts it again and retries once, and a process that cannot
// start fails the call. Callers already retry and report a failing forge, so
// nothing is dropped silently.
type Forge struct {
	host *extension.Host
	inst Instance
	log  *slog.Logger

	mu     sync.Mutex
	client forgev1.ForgeServiceClient
}

var (
	_ forge.Forge                   = (*Forge)(nil)
	_ forge.PullRequestReader       = (*Forge)(nil)
	_ forge.PullRequestDiffReader   = (*Forge)(nil)
	_ forge.RepoArchiveReader       = (*Forge)(nil)
	_ forge.PullRequestReviewReader = (*Forge)(nil)
	_ forge.ReviewCommentWriter     = (*Forge)(nil)
	_ forge.WebhookParser           = (*Forge)(nil)
)

// Open starts the instance's extension process and configures it. The process
// runs until Close is called, whatever happens to ctx.
func Open(ctx context.Context, host *extension.Host, inst Instance, log *slog.Logger) (*Forge, error) {
	f := &Forge{host: host, inst: inst, log: log}
	if _, err := f.connect(ctx, nil); err != nil {
		return nil, err
	}
	return f, nil
}

// Close stops the extension process.
func (f *Forge) Close() { f.host.Stop(f.inst.Spec.Name) }

// connect returns the live client, starting the process when stale is nil or
// is the client the caller just saw fail.
func (f *Forge) connect(ctx context.Context, stale forgev1.ForgeServiceClient) (forgev1.ForgeServiceClient, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.client != nil && f.client != stale && f.host.Alive(f.inst.Spec.Name) {
		return f.client, nil
	}
	raw, err := f.host.Start(context.WithoutCancel(ctx), f.inst.Spec, Surface, &Plugin{})
	if err != nil {
		return nil, err
	}
	client, ok := raw.(forgev1.ForgeServiceClient)
	if !ok {
		f.host.Stop(f.inst.Spec.Name)
		return nil, fmt.Errorf("extension %q: unexpected client type %T", f.inst.Spec.Name, raw)
	}
	if _, err := client.Configure(ctx, &forgev1.ConfigureRequest{Host: f.inst.Host, Token: f.inst.Token, Settings: f.inst.Settings}); err != nil {
		f.host.Stop(f.inst.Spec.Name)
		return nil, fmt.Errorf("extension %q: configure: %w", f.inst.Spec.Name, err)
	}
	f.client = client
	return client, nil
}

// call runs fn on the live client, restarting the process once when the call
// finds it gone.
func call[T any](ctx context.Context, f *Forge, op string, fn func(forgev1.ForgeServiceClient) (T, error)) (T, error) {
	var zero T
	client, err := f.connect(ctx, nil)
	if err != nil {
		return zero, fmt.Errorf("forge %s: %w", op, err)
	}
	out, err := fn(client)
	if status.Code(err) == codes.Unavailable {
		if client, err = f.connect(ctx, client); err != nil {
			return zero, fmt.Errorf("forge %s: %w", op, err)
		}
		out, err = fn(client)
	}
	if status.Code(err) == codes.Unimplemented {
		return zero, fmt.Errorf("this forge does not support %s", op)
	}
	if err != nil {
		return zero, fmt.Errorf("forge %s: %w", op, err)
	}
	return out, nil
}

func ref(owner, repo string) *forgev1.RepoRef { return &forgev1.RepoRef{Owner: owner, Repo: repo} }

func prRef(owner, repo string, number int) *forgev1.PullRequestRef {
	return &forgev1.PullRequestRef{Repo: ref(owner, repo), Number: int32(number)} //nolint:gosec // issue numbers fit int32
}

func issues(in []*forgev1.Issue) []forge.Issue {
	out := make([]forge.Issue, 0, len(in))
	for _, i := range in {
		out = append(out, forge.Issue{Number: int(i.GetNumber()), Title: i.GetTitle(), Body: i.GetBody(), Labels: i.GetLabels()})
	}
	return out
}

func (f *Forge) AssignedIssues(ctx context.Context, owner, repo, assignee string) ([]forge.Issue, error) {
	resp, err := call(ctx, f, "assigned issues", func(c forgev1.ForgeServiceClient) (*forgev1.AssignedIssuesResponse, error) {
		return c.AssignedIssues(ctx, &forgev1.AssignedIssuesRequest{Repo: ref(owner, repo), Assignee: assignee})
	})
	if err != nil {
		return nil, err
	}
	return issues(resp.GetIssues()), nil
}

func (f *Forge) IssuesWithLabel(ctx context.Context, owner, repo, label string) ([]forge.Issue, error) {
	resp, err := call(ctx, f, "issues with label", func(c forgev1.ForgeServiceClient) (*forgev1.IssuesWithLabelResponse, error) {
		return c.IssuesWithLabel(ctx, &forgev1.IssuesWithLabelRequest{Repo: ref(owner, repo), Label: label})
	})
	if err != nil {
		return nil, err
	}
	return issues(resp.GetIssues()), nil
}

func (f *Forge) Comment(ctx context.Context, owner, repo string, number int, body string) (int64, error) {
	resp, err := call(ctx, f, "comment", func(c forgev1.ForgeServiceClient) (*forgev1.CommentResponse, error) {
		return c.Comment(ctx, &forgev1.CommentRequest{Repo: ref(owner, repo), Number: int32(number), Body: body}) //nolint:gosec // issue numbers fit int32
	})
	return resp.GetId(), err
}

func (f *Forge) CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error {
	_, err := call(ctx, f, "close issue", func(c forgev1.ForgeServiceClient) (*forgev1.CloseIssueResponse, error) {
		return c.CloseIssue(ctx, &forgev1.CloseIssueRequest{Repo: ref(owner, repo), Number: int32(number), Comment: comment}) //nolint:gosec // issue numbers fit int32
	})
	return err
}

func (f *Forge) React(ctx context.Context, owner, repo string, number int, reaction string) error {
	_, err := call(ctx, f, "react", func(c forgev1.ForgeServiceClient) (*forgev1.ReactResponse, error) {
		return c.React(ctx, &forgev1.ReactRequest{Repo: ref(owner, repo), Number: int32(number), Reaction: reaction}) //nolint:gosec // issue numbers fit int32
	})
	return err
}

// SetStateLabel has no error to return; a failure is logged and the next
// state change corrects the label.
func (f *Forge) SetStateLabel(ctx context.Context, owner, repo string, number int, label string, knownLabels []string) {
	_, err := call(ctx, f, "set state label", func(c forgev1.ForgeServiceClient) (*forgev1.SetStateLabelResponse, error) {
		return c.SetStateLabel(ctx, &forgev1.SetStateLabelRequest{Repo: ref(owner, repo), Number: int32(number), Label: label, KnownLabels: knownLabels}) //nolint:gosec // issue numbers fit int32
	})
	if err != nil {
		f.log.Warn("set state label", "owner", owner, "repo", repo, "number", number, "err", err)
	}
}

func (f *Forge) CreatePR(ctx context.Context, owner, repo, title, head, base, body string) (int, error) {
	resp, err := call(ctx, f, "create pull request", func(c forgev1.ForgeServiceClient) (*forgev1.CreatePRResponse, error) {
		return c.CreatePR(ctx, &forgev1.CreatePRRequest{Repo: ref(owner, repo), Title: title, Head: head, Base: base, Body: body})
	})
	return int(resp.GetNumber()), err
}

func (f *Forge) ClosePR(ctx context.Context, owner, repo string, number int, comment string) error {
	_, err := call(ctx, f, "close pull request", func(c forgev1.ForgeServiceClient) (*forgev1.ClosePRResponse, error) {
		return c.ClosePR(ctx, &forgev1.ClosePRRequest{Repo: ref(owner, repo), Number: int32(number), Comment: comment}) //nolint:gosec // PR numbers fit int32
	})
	return err
}

func (f *Forge) MergePR(ctx context.Context, owner, repo string, number int) error {
	_, err := call(ctx, f, "merge pull request", func(c forgev1.ForgeServiceClient) (*forgev1.MergePRResponse, error) {
		return c.MergePR(ctx, &forgev1.MergePRRequest{Repo: ref(owner, repo), Number: int32(number)}) //nolint:gosec // PR numbers fit int32
	})
	return err
}

func (f *Forge) PRState(ctx context.Context, owner, repo string, number int) (string, error) {
	resp, err := call(ctx, f, "pull request state", func(c forgev1.ForgeServiceClient) (*forgev1.PRStateResponse, error) {
		return c.PRState(ctx, &forgev1.PRStateRequest{Repo: ref(owner, repo), Number: int32(number)}) //nolint:gosec // issue numbers fit int32
	})
	return resp.GetState(), err
}

func (f *Forge) GetPullRequest(ctx context.Context, owner, repo string, number int) (forge.PullRequest, error) {
	resp, err := call(ctx, f, "get pull request", func(c forgev1.ForgeServiceClient) (*forgev1.GetPullRequestResponse, error) {
		return c.GetPullRequest(ctx, &forgev1.GetPullRequestRequest{Repo: ref(owner, repo), Number: int32(number)}) //nolint:gosec // issue numbers fit int32
	})
	if err != nil {
		return forge.PullRequest{}, err
	}
	pr := resp.GetPullRequest()
	return forge.PullRequest{
		Number: int(pr.GetNumber()), Title: pr.GetTitle(), Body: pr.GetBody(),
		HeadRef: pr.GetHeadRef(), BaseRef: pr.GetBaseRef(), HeadSHA: pr.GetHeadSha(), BaseSHA: pr.GetBaseSha(),
		State: pr.GetState(),
	}, nil
}

func (f *Forge) GetPullRequestDiff(ctx context.Context, owner, repo string, number int) (string, error) {
	resp, err := call(ctx, f, "get pull request diff", func(c forgev1.ForgeServiceClient) (*forgev1.GetPullRequestDiffResponse, error) {
		return c.GetPullRequestDiff(ctx, &forgev1.GetPullRequestDiffRequest{Repo: ref(owner, repo), Number: int32(number)}) //nolint:gosec // issue numbers fit int32
	})
	return resp.GetDiff(), err
}

func (f *Forge) ListReviews(ctx context.Context, owner, repo string, number int, sinceID int64) ([]forge.Review, error) {
	resp, err := call(ctx, f, "list reviews", func(c forgev1.ForgeServiceClient) (*forgev1.ListReviewsResponse, error) {
		return c.ListReviews(ctx, &forgev1.ListReviewsRequest{PullRequest: prRef(owner, repo, number), SinceId: sinceID})
	})
	if err != nil {
		return nil, err
	}
	out := make([]forge.Review, 0, len(resp.GetReviews()))
	for _, r := range resp.GetReviews() {
		out = append(out, forge.Review{ID: r.GetId(), Author: r.GetAuthor(), State: r.GetState(), Body: r.GetBody(), SubmittedAt: r.GetSubmittedAt().AsTime()})
	}
	return out, nil
}

func (f *Forge) ListReviewComments(ctx context.Context, owner, repo string, number int, sinceID int64) ([]forge.ReviewComment, error) {
	resp, err := call(ctx, f, "list review comments", func(c forgev1.ForgeServiceClient) (*forgev1.ListReviewCommentsResponse, error) {
		return c.ListReviewComments(ctx, &forgev1.ListReviewCommentsRequest{PullRequest: prRef(owner, repo, number), SinceId: sinceID})
	})
	if err != nil {
		return nil, err
	}
	out := make([]forge.ReviewComment, 0, len(resp.GetComments()))
	for _, c := range resp.GetComments() {
		out = append(out, forge.ReviewComment{
			ID: c.GetId(), ReviewID: c.GetReviewId(), Author: c.GetAuthor(), Body: c.GetBody(), Path: c.GetPath(),
			Line: int(c.GetLine()), InReplyTo: c.GetInReplyTo(), CreatedAt: c.GetCreatedAt().AsTime(),
		})
	}
	return out, nil
}

func (f *Forge) ReplyToReview(ctx context.Context, owner, repo string, number int, commentID int64, body string) error {
	_, err := call(ctx, f, "reply to review", func(c forgev1.ForgeServiceClient) (*forgev1.ReplyToReviewResponse, error) {
		return c.ReplyToReview(ctx, &forgev1.ReplyToReviewRequest{PullRequest: prRef(owner, repo, number), CommentId: commentID, Body: body})
	})
	return err
}

func (f *Forge) CreateReviewComments(ctx context.Context, owner, repo string, number int, reviewedHeadSHA string, comments []forge.InlineReviewComment) error {
	in := make([]*forgev1.InlineReviewComment, 0, len(comments))
	for _, c := range comments {
		in = append(in, &forgev1.InlineReviewComment{Path: c.Path, Line: int32(c.Line), Body: c.Body}) //nolint:gosec // line numbers fit int32
	}
	_, err := call(ctx, f, "create review comments", func(c forgev1.ForgeServiceClient) (*forgev1.CreateReviewCommentsResponse, error) {
		return c.CreateReviewComments(ctx, &forgev1.CreateReviewCommentsRequest{PullRequest: prRef(owner, repo, number), ReviewedHeadSha: reviewedHeadSHA, Comments: in})
	})
	return err
}

func (f *Forge) AcceptInvitations(ctx context.Context) error {
	_, err := call(ctx, f, "accept invitations", func(c forgev1.ForgeServiceClient) (*forgev1.AcceptInvitationsResponse, error) {
		return c.AcceptInvitations(ctx, &forgev1.AcceptInvitationsRequest{})
	})
	return err
}

func (f *Forge) VerifyPush(ctx context.Context, owner, repo string) error {
	_, err := call(ctx, f, "verify push", func(c forgev1.ForgeServiceClient) (*forgev1.VerifyPushResponse, error) {
		return c.VerifyPush(ctx, &forgev1.VerifyPushRequest{Repo: ref(owner, repo)})
	})
	return err
}

func (f *Forge) LinkBranch(ctx context.Context, owner, repo string, issueNumber int, branch string) error {
	_, err := call(ctx, f, "link branch", func(c forgev1.ForgeServiceClient) (*forgev1.LinkBranchResponse, error) {
		return c.LinkBranch(ctx, &forgev1.LinkBranchRequest{Repo: ref(owner, repo), IssueNumber: int32(issueNumber), Branch: branch}) //nolint:gosec // issue numbers fit int32
	})
	return err
}

// ParseWebhook has the extension decode an authenticated delivery.
func (f *Forge) ParseWebhook(ctx context.Context, headers map[string]string, body []byte) (forge.WebhookEvent, error) {
	resp, err := call(ctx, f, "parse webhook", func(c forgev1.ForgeServiceClient) (*forgev1.ParseWebhookResponse, error) {
		return c.ParseWebhook(ctx, &forgev1.ParseWebhookRequest{Headers: headers, Body: body})
	})
	if status.Code(errors.Unwrap(err)) == codes.InvalidArgument {
		return forge.WebhookEvent{}, fmt.Errorf("%w: %w", forge.ErrBadWebhook, err)
	}
	if err != nil {
		return forge.WebhookEvent{}, err
	}
	switch e := resp.GetEvent().(type) {
	case *forgev1.ParseWebhookResponse_Issue:
		i := e.Issue
		return forge.WebhookEvent{Issue: &forge.IssueEvent{
			Action: i.GetAction(), State: i.GetState(), PullRequest: i.GetIsPullRequest(),
			Owner: i.GetRepo().GetOwner(), Repo: i.GetRepo().GetRepo(), Number: int(i.GetNumber()),
			Title: i.GetTitle(), Body: i.GetBody(), Labels: i.GetLabels(), Assignees: i.GetAssignees(),
		}}, nil
	case *forgev1.ParseWebhookResponse_Review:
		r := e.Review
		return forge.WebhookEvent{Review: &forge.ReviewEvent{
			Action: r.GetAction(), Owner: r.GetRepo().GetOwner(), Repo: r.GetRepo().GetRepo(), PRNumber: int(r.GetPrNumber()),
			ReviewID: r.GetReviewId(), Author: r.GetAuthor(), State: r.GetState(), Body: r.GetBody(),
		}}, nil
	case *forgev1.ParseWebhookResponse_ReviewComment:
		c := e.ReviewComment
		return forge.WebhookEvent{ReviewComment: &forge.ReviewCommentEvent{
			Action: c.GetAction(), Owner: c.GetRepo().GetOwner(), Repo: c.GetRepo().GetRepo(), PRNumber: int(c.GetPrNumber()),
			ReviewID: c.GetReviewId(), CommentID: c.GetCommentId(), Author: c.GetAuthor(), Body: c.GetBody(),
			Path: c.GetPath(), Line: int(c.GetLine()),
		}}, nil
	}
	return forge.WebhookEvent{}, nil
}

// GetRepoArchive streams the archive. The first chunk is read before
// returning so an unsupported or failing forge surfaces as the call's error.
func (f *Forge) GetRepoArchive(ctx context.Context, owner, repo, ref string) (io.ReadCloser, error) {
	ctx, cancel := context.WithCancel(ctx)
	stream, err := call(ctx, f, "get repo archive", func(c forgev1.ForgeServiceClient) (grpc.ServerStreamingClient[forgev1.GetRepoArchiveResponse], error) {
		return c.GetRepoArchive(ctx, &forgev1.GetRepoArchiveRequest{Repo: &forgev1.RepoRef{Owner: owner, Repo: repo}, Ref: ref})
	})
	if err != nil {
		cancel()
		return nil, err
	}
	first, err := stream.Recv()
	if err != nil && !errors.Is(err, io.EOF) {
		cancel()
		if status.Code(err) == codes.Unimplemented {
			return nil, errors.New("this forge does not support get repo archive")
		}
		return nil, fmt.Errorf("forge get repo archive: %w", err)
	}
	pr, pw := io.Pipe()
	go func() {
		defer cancel()
		if err == nil {
			if _, werr := pw.Write(first.GetData()); werr != nil {
				return
			}
			for {
				chunk, rerr := stream.Recv()
				if errors.Is(rerr, io.EOF) {
					break
				}
				if rerr != nil {
					_ = pw.CloseWithError(rerr)
					return
				}
				if _, werr := pw.Write(chunk.GetData()); werr != nil {
					return
				}
			}
		}
		_ = pw.Close()
	}()
	return pr, nil
}
