package controlplane

import (
	"github.com/samcharles93/archie-core/internal/config"
)

// ReviewSettingsKind is the control-plane resource holding the pr-review
// workflow's two dials: whether a post-worthiness pass runs before findings are
// posted, and whether a human approves the draft before it posts. The file's
// [review] section seeds it; the stored document outranks the file from then on
const ReviewSettingsKind = "review-settings"

type reviewSettings struct {
	PrecisionGate     bool `json:"precision_gate" title:"Precision gate" doc:"Run a post-worthiness pass before findings are posted."`
	ApproveBeforePost bool `json:"approve_before_post" title:"Approve before posting" doc:"Park a finished review for operator approval before it posts."`
}

func reviewSettingsDefinition() Definition {
	return Definition{
		Kind:      ReviewSettingsKind,
		Title:     "Review settings",
		ApplyMode: "live",
		Document:  reviewSettings{},
		Seed: func(cfg config.Config) any {
			return reviewSettings{PrecisionGate: cfg.Review.PrecisionGate, ApproveBeforePost: cfg.Review.ApproveBeforePost}
		},
		Validate: validateReviewSettings,
	}
}

// validateReviewSettings accepts every value.
func validateReviewSettings(input []byte) error {
	return validateAs(input, func(reviewSettings) error { return nil })
}
