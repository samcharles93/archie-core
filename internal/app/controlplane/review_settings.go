package controlplane

import (
	"github.com/samcharles93/archie-core/internal/config"
)

// ReviewSettingsKind is the control-plane resource holding the pr-review
// workflow's two dials: whether a post-worthiness pass runs before findings are
// posted, and whether a human approves the draft before it posts. The file's
// [review] section seeds it; the stored document outranks the file from then on
// (docs/prds/review-settings-resource.md).
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

// validateReviewSettings accepts every value, deliberately: both dials are free
// booleans with no cross-field rule, and a stored value reaches its consumers
// through the same layered cfg.Review boot validates. It stays a real function
// rather than an inline no-op so a rule, if one is ever settled, has one
// obvious home (validatePluginSettings is the precedent).
func validateReviewSettings(input []byte) error {
	return validateAs(input, func(reviewSettings) error { return nil })
}
