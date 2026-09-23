package daemon

import (
	"context"
	"errors"
	"testing"

	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/forge"
)

type fakeReviewReader struct {
	reviews  []forge.Review
	comments []forge.ReviewComment
	listErr  error
}

func (f *fakeReviewReader) ListReviews(ctx context.Context, owner, repo string, number int, sinceID int64) ([]forge.Review, error) {
	// The real implementations filter on the forge; the fake models the
	// contract so "records at or below the cursor never come back" is
	// actually exercised.
	var out []forge.Review
	for _, r := range f.reviews {
		if r.ID > sinceID {
			out = append(out, r)
		}
	}
	return out, f.listErr
}

func (f *fakeReviewReader) ListReviewComments(ctx context.Context, owner, repo string, number int, sinceID int64) ([]forge.ReviewComment, error) {
	var out []forge.ReviewComment
	for _, c := range f.comments {
		if c.ID > sinceID {
			out = append(out, c)
		}
	}
	return out, f.listErr
}

func (f *fakeReviewReader) ReplyToReview(ctx context.Context, owner, repo string, number int, inReplyTo int64, body string) error {
	return nil
}

type recordingPublisher struct {
	reactions []workintake.ReviewCommentEnvelope
	err       error
}

func (p *recordingPublisher) publish(ctx context.Context, e workintake.ReviewCommentEnvelope) error {
	if p.err != nil {
		return p.err
	}
	p.reactions = append(p.reactions, e)
	return nil
}

func openTask(prNumber int, reviewCursor, commentCursor int64) *workflowtask.Task {
	return &workflowtask.Task{ID: 7, Owner: "acme", Repo: "widgets", PRNumber: prNumber, Status: "pr_open", ReviewCursor: reviewCursor, WatchCommentID: commentCursor}
}

func TestScanPRReviewReactionsPublishesNewRecordsAndSkipsOwn(t *testing.T) {
	reader := &fakeReviewReader{
		reviews: []forge.Review{
			{ID: 5, Author: "archie-bot", State: "requested_changes", Body: "mine"},
			{ID: 7, Author: "alice", State: "requested_changes", Body: "please fix"},
		},
		comments: []forge.ReviewComment{
			{ID: 9, ReviewID: 7, Author: "archie-bot", Body: "my reply"},
			{ID: 11, ReviewID: 7, Author: "bob", Body: "also this", Path: "x.go", Line: 3},
		},
	}
	publisher := &recordingPublisher{}
	task := openTask(42, 0, 0)

	reviewCursor, commentCursor, err := scanPRReviewReactions(
		context.Background(), reader, task, "archie-bot", 0, 0, publisher.publish)
	if err != nil {
		t.Fatal(err)
	}

	// The own review and own reply are excluded; alice's review and bob's
	// comment are the two actionable records.
	if len(publisher.reactions) != 2 {
		t.Fatalf("published = %d, want 2", len(publisher.reactions))
	}
	if publisher.reactions[0].Kind != workintake.ReviewReactionReview || publisher.reactions[0].ReviewID != 7 {
		t.Errorf("first reaction = %+v, want alice's review 7", publisher.reactions[0])
	}
	if publisher.reactions[1].Kind != workintake.ReviewReactionComment || publisher.reactions[1].CommentID != 11 {
		t.Errorf("second reaction = %+v, want bob's comment 11", publisher.reactions[1])
	}
	if publisher.reactions[1].ReviewID != 7 {
		t.Errorf("comment reaction ReviewID = %d, want 7 (collected under its review)", publisher.reactions[1].ReviewID)
	}
	if publisher.reactions[1].Body != "also this" || publisher.reactions[1].Path != "x.go" || publisher.reactions[1].Line != 3 {
		t.Errorf("comment payload = %+v, want the comment's own content", publisher.reactions[1])
	}
	// Cursors advance past every record seen, own or not: a cursor that
	// stalls on a skipped record would re-list it every scan forever.
	if reviewCursor != 7 || commentCursor != 11 {
		t.Errorf("cursors = (%d, %d), want (7, 11)", reviewCursor, commentCursor)
	}
}

func TestScanPRReviewReactionsSkipRecordsAtOrBelowTheCursors(t *testing.T) {
	reader := &fakeReviewReader{
		reviews:  []forge.Review{{ID: 5, Author: "alice", State: "requested_changes", Body: "old"}},
		comments: []forge.ReviewComment{{ID: 6, ReviewID: 5, Author: "bob", Body: "old comment"}},
	}
	publisher := &recordingPublisher{}

	reviewCursor, commentCursor, err := scanPRReviewReactions(
		context.Background(), reader, openTask(42, 5, 6), "archie-bot", 5, 6, publisher.publish)
	if err != nil {
		t.Fatal(err)
	}
	if len(publisher.reactions) != 0 {
		t.Errorf("published %d reactions for records at or below the cursors", len(publisher.reactions))
	}
	if reviewCursor != 5 || commentCursor != 6 {
		t.Errorf("cursors = (%d, %d), want unchanged (5, 6)", reviewCursor, commentCursor)
	}
}

func TestScanPRReviewReactionsKeepsCursorsOnPublishFailure(t *testing.T) {
	reader := &fakeReviewReader{
		reviews: []forge.Review{{ID: 7, Author: "alice", State: "requested_changes", Body: "fix"}},
	}
	publisher := &recordingPublisher{err: errors.New("bus unavailable")}

	_, _, err := scanPRReviewReactions(
		context.Background(), reader, openTask(42, 0, 0), "archie-bot", 0, 0, publisher.publish)
	if err == nil {
		t.Fatal("publish error was swallowed; the cursors must not advance past unpublished records")
	}
}
