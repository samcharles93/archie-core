package workintake

import (
	"errors"
	"reflect"
	"testing"
)

func TestReviewCommentEnvelopeRoundTrip(t *testing.T) {
	tests := []ReviewCommentEnvelope{
		{
			Owner: "acme", Repo: "widgets", PRNumber: 42,
			Kind: ReviewReactionReview, ReviewID: 17, Author: "reviewer",
			State: "requested_changes", Body: "Please handle nil input.",
		},
		{
			Owner: "acme", Repo: "widgets", PRNumber: 42,
			Kind: ReviewReactionComment, CommentID: 17, ReviewID: 9,
			Author: "reviewer", Body: "This branch leaks.", Path: "worker.go", Line: 73,
		},
	}
	for _, want := range tests {
		data, err := want.Encode()
		if err != nil {
			t.Fatalf("Encode(%q) = %v", want.Kind, err)
		}
		got, err := DecodeReviewComment(data)
		if err != nil {
			t.Fatalf("DecodeReviewComment(%q) = %v", want.Kind, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("round trip %q = %+v, want %+v", want.Kind, got, want)
		}
	}
}

func TestReviewCommentEnvelopeKindSeparatesIdempotencyKeys(t *testing.T) {
	review := ReviewCommentEnvelope{Owner: "acme", Repo: "widgets", PRNumber: 42, Kind: ReviewReactionReview, ReviewID: 17}
	comment := ReviewCommentEnvelope{Owner: "acme", Repo: "widgets", PRNumber: 42, Kind: ReviewReactionComment, CommentID: 17}

	if review.IdempotencyKey() == comment.IdempotencyKey() {
		t.Fatalf("review and comment idempotency keys both %q; independent ID sequences collided", review.IdempotencyKey())
	}
	if got, want := review.IdempotencyKey(), "archie:review/default/acme/widgets/42/review/17"; got != want {
		t.Errorf("review key = %q, want %q", got, want)
	}
	if got, want := comment.IdempotencyKey(), "archie:review/default/acme/widgets/42/comment/17"; got != want {
		t.Errorf("comment key = %q, want %q", got, want)
	}
	// A resolved org is carried into the key.
	review.Org = "soc"
	if got, want := review.IdempotencyKey(), "archie:review/soc/acme/widgets/42/review/17"; got != want {
		t.Errorf("org-scoped review key = %q, want %q", got, want)
	}
}

func TestReviewCommentEnvelopeValidation(t *testing.T) {
	tests := []struct {
		name string
		env  ReviewCommentEnvelope
	}{
		{name: "missing coordinates", env: ReviewCommentEnvelope{Kind: ReviewReactionReview, ReviewID: 1}},
		{name: "unknown kind", env: ReviewCommentEnvelope{Owner: "o", Repo: "r", PRNumber: 1, Kind: "reaction", ReviewID: 1}},
		{name: "review missing review id", env: ReviewCommentEnvelope{Owner: "o", Repo: "r", PRNumber: 1, Kind: ReviewReactionReview}},
		{name: "comment missing comment id", env: ReviewCommentEnvelope{Owner: "o", Repo: "r", PRNumber: 1, Kind: ReviewReactionComment}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.env.Validate(); !errors.Is(err, ErrInvalidReviewReaction) {
				t.Fatalf("Validate() = %v, want ErrInvalidReviewReaction", err)
			}
		})
	}

	valid := ReviewCommentEnvelope{Owner: "o", Repo: "r", PRNumber: 1, Kind: ReviewReactionComment, CommentID: 2}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}
	if got := valid.Subject(); got != SubjectReviewReaction {
		t.Errorf("Subject() = %q, want %q", got, SubjectReviewReaction)
	}
	if got := valid.Ref(); got != "o/r#1" {
		t.Errorf("Ref() = %q, want o/r#1", got)
	}
}
