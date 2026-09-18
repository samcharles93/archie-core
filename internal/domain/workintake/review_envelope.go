package workintake

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// ErrInvalidReviewReaction reports a review reaction that cannot be routed.
var ErrInvalidReviewReaction = errors.New("workintake: invalid review reaction")

// ReviewReactionKind distinguishes whole reviews from their comment records.
type ReviewReactionKind string

const (
	ReviewReactionReview  ReviewReactionKind = "review"
	ReviewReactionComment ReviewReactionKind = "comment"

	// SubjectReviewReaction carries forge review activity for Archie-owned PRs.
	SubjectReviewReaction = "archie.reaction.review_comment"
)

// ReviewCommentEnvelope is the forge-neutral reaction payload for a whole
// review or one review comment.
type ReviewCommentEnvelope struct {
	Owner     string             `json:"owner"`
	Repo      string             `json:"repo"`
	PRNumber  int                `json:"pr_number"`
	Kind      ReviewReactionKind `json:"kind"`
	CommentID int64              `json:"comment_id,omitempty"`
	ReviewID  int64              `json:"review_id,omitempty"`
	Author    string             `json:"author,omitempty"`
	State     string             `json:"state,omitempty"`
	Body      string             `json:"body,omitempty"`
	Path      string             `json:"path,omitempty"`
	Line      int                `json:"line,omitempty"`
}

// Validate reports whether the envelope has the coordinates and external ID
// its reaction kind requires.
func (e ReviewCommentEnvelope) Validate() error {
	if e.Owner == "" || e.Repo == "" || e.PRNumber <= 0 {
		return fmt.Errorf("%w: owner, repo and positive PR number are required", ErrInvalidReviewReaction)
	}
	switch e.Kind {
	case ReviewReactionReview:
		if e.ReviewID <= 0 {
			return fmt.Errorf("%w: review id is required for kind %q", ErrInvalidReviewReaction, e.Kind)
		}
	case ReviewReactionComment:
		if e.CommentID <= 0 {
			return fmt.Errorf("%w: comment id is required for kind %q", ErrInvalidReviewReaction, e.Kind)
		}
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidReviewReaction, e.Kind)
	}
	return nil
}

// Encode renders the envelope for transport.
func (e ReviewCommentEnvelope) Encode() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("encode review reaction: %w", err)
	}
	return data, nil
}

// DecodeReviewComment parses a transported review envelope.
func DecodeReviewComment(data []byte) (ReviewCommentEnvelope, error) {
	var e ReviewCommentEnvelope
	if err := json.Unmarshal(data, &e); err != nil {
		return ReviewCommentEnvelope{}, fmt.Errorf("decode review reaction: %w", err)
	}
	if err := e.Validate(); err != nil {
		return ReviewCommentEnvelope{}, err
	}
	return e, nil
}

// Ref identifies the pull request receiving the reaction.
func (e ReviewCommentEnvelope) Ref() string {
	return e.Owner + "/" + e.Repo + "#" + strconv.Itoa(e.PRNumber)
}

// IdempotencyKey identifies one forge review record independently of delivery
// source.
func (e ReviewCommentEnvelope) IdempotencyKey() string {
	id := e.ReviewID
	if e.Kind == ReviewReactionComment {
		id = e.CommentID
	}
	return dedupKeyPrefix + "review/" + e.Owner + "/" + e.Repo + "/" + strconv.Itoa(e.PRNumber) + "/" + string(e.Kind) + "/" + strconv.FormatInt(id, 10)
}

// Subject returns the reaction address for this envelope.
func (e ReviewCommentEnvelope) Subject() string { return SubjectReviewReaction }
