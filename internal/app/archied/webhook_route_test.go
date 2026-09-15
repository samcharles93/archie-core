package archied

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
)

// TestWebhookRoutesAppliesConfiguredFields is the red case for
// channels-platform-3: RouteConfig.Secret/Template/DeliverTo are
// implemented and tested in internal/channels/webhook, but the only
// production construction (setupWebhookGateway) built
// []webhook.RouteConfig{{Path: "/webhook"}} literally, so no config value
// could ever reach them and HMAC validation never ran. webhookRoutes is the
// translation site; this pins that a configured [chat.webhook] route
// actually reaches the gateway's RouteConfig.
func TestWebhookRoutesAppliesConfiguredFields(t *testing.T) {
	route := config.WebhookRoute{
		Path:      "/github",
		Template:  "issue.title",
		DeliverTo: "origin",
	}
	got := webhookRoutes(route, "shhh")
	if len(got) != 1 {
		t.Fatalf("webhookRoutes() returned %d routes, want 1", len(got))
	}
	want := got[0]
	if want.Path != "/github" {
		t.Errorf("Path = %q, want %q", want.Path, "/github")
	}
	if want.Secret != "shhh" {
		t.Errorf("Secret = %q, want %q", want.Secret, "shhh")
	}
	if want.Template != "issue.title" {
		t.Errorf("Template = %q, want %q", want.Template, "issue.title")
	}
	if want.DeliverTo != "origin" {
		t.Errorf("DeliverTo = %q, want %q", want.DeliverTo, "origin")
	}
}

// TestWebhookRoutesDefaultsPath proves an unset route path keeps the
// gateway's pre-existing default of "/webhook" rather than becoming
// unreachable.
func TestWebhookRoutesDefaultsPath(t *testing.T) {
	got := webhookRoutes(config.WebhookRoute{}, "")
	if len(got) != 1 || got[0].Path != "/webhook" {
		t.Fatalf("webhookRoutes(zero value) = %+v, want Path=/webhook", got)
	}
}
