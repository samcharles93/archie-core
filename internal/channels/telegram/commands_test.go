package telegram

import (
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// The published menu and /help must describe the commands Telegram can actually
// execute, no more and no less. A command that is typeable but unpublished is
// undiscoverable; a published command with no handler is a lie. Both sides are
// derived from their real sources -- the shared router's LocalCommands and the
// local handler table -- so adding either without the other fails here.
func TestPublishedCommandsMatchExecutableSurface(t *testing.T) {
	executable := make(map[string]string)
	for _, command := range messaging.LocalCommands() {
		executable[strings.TrimPrefix(command, "/")] = "the shared router"
	}
	for _, handler := range telegramTextHandlers {
		executable[strings.TrimPrefix(handler.command, "/")] = "a Telegram handler"
	}

	published := make(map[string]bool, len(gatewayCommandSpecs))
	for _, spec := range gatewayCommandSpecs {
		if published[spec.Command] {
			t.Errorf("gatewayCommandSpecs publishes /%s twice", spec.Command)
		}
		published[spec.Command] = true
	}

	for command, source := range executable {
		if !published[command] {
			t.Errorf("/%s is executable via %s but missing from the menu and /help", command, source)
		}
	}
	for command := range published {
		if _, ok := executable[command]; !ok {
			t.Errorf("/%s is published but nothing on Telegram executes it", command)
		}
	}
}

// TestStatusAndTasksCopyMatchesWhatEachCommandDoes pins the operator-facing
// division of labour archie-core-wp9s restored: /status reports daemon health
// and says nothing about the task list, /tasks reports the work view and says
// nothing about daemon health. The menu and /help are the only place an
// operator learns which of the two to reach for, so copy that promises the
// other command's job sends them to the wrong one -- which is exactly what
// "Show task counts by state" did before Phase 1.
//
// Both published surfaces are covered, because both are "registered with the
// platform": Telegram's own menu/help specs, and the shared messaging specs
// every other adapter and the dashboard's command palette render from. The two
// are also required to agree for these two commands: a menu that describes
// /status differently from the palette is the same lie told twice.
func TestStatusAndTasksCopyMatchesWhatEachCommandDoes(t *testing.T) {
	telegramCopy := make(map[string]string, len(gatewayCommandSpecs))
	for _, spec := range gatewayCommandSpecs {
		telegramCopy[spec.Command] = spec.Description
	}
	sharedCopy := make(map[string]string, len(messaging.LocalCommandSpecs()))
	for _, spec := range messaging.LocalCommandSpecs() {
		sharedCopy[strings.TrimPrefix(spec.Command, "/")] = spec.Description
	}

	surfaces := []struct {
		name string
		copy map[string]string
	}{
		{name: "telegram menu and help", copy: telegramCopy},
		{name: "shared command specs", copy: sharedCopy},
	}

	for _, surface := range surfaces {
		t.Run(surface.name, func(t *testing.T) {
			status := strings.ToLower(surface.copy["status"])
			tasks := strings.ToLower(surface.copy["tasks"])

			if !strings.Contains(status, "health") {
				t.Errorf("/status is published as %q, want it described as the health surface", status)
			}
			if strings.Contains(status, "task") {
				t.Errorf("/status is published as %q, want no promise of a task view -- that is /tasks' job", status)
			}
			if !strings.Contains(tasks, "task") {
				t.Errorf("/tasks is published as %q, want it described as the task view", tasks)
			}
			if strings.Contains(tasks, "health") {
				t.Errorf("/tasks is published as %q, want no promise of daemon health -- that is /status' job", tasks)
			}
		})
	}

	for _, cmd := range []string{"status", "tasks"} {
		if telegramCopy[cmd] != sharedCopy[cmd] {
			t.Errorf("/%s description differs between the Telegram menu (%q) and the shared specs (%q)",
				cmd, telegramCopy[cmd], sharedCopy[cmd])
		}
	}
}
