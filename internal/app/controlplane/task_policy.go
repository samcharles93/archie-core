package controlplane

import (
	"fmt"
	"net/url"

	"github.com/samcharles93/archie-core/internal/config"
)

const TaskPolicyKind = "task-policy"

type taskPolicy struct {
	DiffCapLines  int    `json:"diff_cap_lines"`
	NotifyWebhook string `json:"notify_webhook"`
}

func taskPolicyDefinition() Definition {
	return Definition{Kind: TaskPolicyKind, Title: "Task policy", ApplyMode: "live", Document: taskPolicy{}, Seed: func(cfg config.Config) any {
		return taskPolicy{DiffCapLines: cfg.DiffCap(), NotifyWebhook: cfg.Notify.Webhook}
	}, Validate: func(input []byte) error {
		return validateAs(input, func(doc taskPolicy) error {
			if doc.DiffCapLines < 0 {
				return fmt.Errorf("diff_cap_lines must not be negative")
			}
			if doc.NotifyWebhook != "" {
				parsed, err := url.Parse(doc.NotifyWebhook)
				if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
					return fmt.Errorf("notify_webhook must be an HTTP or HTTPS URL")
				}
			}
			return nil
		})
	}}
}
