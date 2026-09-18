package workintake

import (
	"encoding/json"
	"fmt"
	"strconv"
)

const (
	ReviewStateApproved         = "approved"
	ReviewStateRequestedChanges = "requested_changes"
	ReviewStateCommented        = "commented"
	ReviewStateDismissed        = "dismissed"
)

// ReviewCommentEnvelope is a review comment reaction on an existing pull
// request. It deliberately carries no workflow or source information: webhook
// and polling producers must produce the same bytes and therefore share
// idempotency and authorization at the consumer.
type ReviewCommentEnvelope struct {
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	PRNumber  int    `json:"pr_number"`
	CommentID int64  `json:"comment_id"`
	ReviewID  int64  `json:"review_id,omitempty"`
	Author    string `json:"author"`
	State     string `json:"state,omitempty"`
	Body      string `json:"body"`
	Path      string `json:"path,omitempty"`
	Line      int    `json:"line,omitempty"`
}

// Encode serializes a review reaction for the event bus.
func (r ReviewCommentEnvelope) Encode() ([]byte, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("encode review comment: %w", err)
	}
	return data, nil
}

// DecodeReviewComment parses a review reaction from the event bus.
func DecodeReviewComment(data []byte) (ReviewCommentEnvelope, error) {
	var r ReviewCommentEnvelope
	if err := json.Unmarshal(data, &r); err != nil {
		return ReviewCommentEnvelope{}, fmt.Errorf("decode review comment: %w", err)
	}
	return r, nil
}

// DecodeReviewCommentEnvelope is the explicit alias used by consumers that
// name the wire type rather than the reaction category.
func DecodeReviewCommentEnvelope(data []byte) (ReviewCommentEnvelope, error) {
	return DecodeReviewComment(data)
}

// Ref identifies the pull request receiving the reaction.
func (r ReviewCommentEnvelope) Ref() string {
	return r.Owner + "/" + r.Repo + "#" + strconv.Itoa(r.PRNumber)
}

// IdempotencyKey is independent of the event source. A comment delivered by
// both GitHub's webhook and the poll backstop is consequently one reaction.
func (r ReviewCommentEnvelope) IdempotencyKey() string {
	return dedupKeyPrefix + r.Owner + "/" + r.Repo + "/" + strconv.Itoa(r.PRNumber) + "/" + strconv.FormatInt(r.CommentID, 10)
}

// Subject returns the dedicated reaction subject rather than an issue task
// subject; consumers resolve the PR to an existing Archie task.
func (r ReviewCommentEnvelope) Subject() string { return SubjectReviewComment }
