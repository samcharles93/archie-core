package main

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/installtype"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

// TestMakeUpdateServiceWiresInstallType is the regression test for
// archie-core-522's fail-closed guarantee actually reaching production: a
// Service built without InstallType always refuses to install (see
// releaseupdate.ErrUnknownInstallType), so wiring it here is what makes
// /update work at all for a correctly-stamped build, not just a detail.
func TestMakeUpdateServiceWiresInstallType(t *testing.T) {
	cfg := config.Config{}
	cfg.Chat.Telegram.UpdateCheckCommand = []string{"archie-update-check"}
	cfg.Chat.Telegram.UpdateInstallCommand = []string{"archie-update-install"}

	service := makeUpdateService(telegramSetup{Cfg: config.NewHolder(cfg)})

	if service == nil {
		t.Fatal("makeUpdateService returned nil despite a configured check command")
	}
	if service.InstallType != installtype.Type() {
		t.Errorf("Service.InstallType = %q, want installtype.Type() = %q", service.InstallType, installtype.Type())
	}
}

// TestDaemonRunningVersionsVouchesOnlyForItselfWithoutAgentStatus: the point
// of checking a pending update report against running versions is that the
// value checked against is ground truth. This process's own compiled-in
// version is; a guess at the agent's is not, because runtimeVersion records
// what archied's release pipeline stamped, not what an agent container is
// actually running. Before any task has reported an observed agent version
// (agentStatus nil, or nothing observed yet), including one would recreate
// the false success the check exists to catch, so its absence is the
// assertion.
func TestDaemonRunningVersionsVouchesOnlyForItselfWithoutAgentStatus(t *testing.T) {
	running := daemonRunningVersions(nil)

	if got := running[releaseupdate.ComponentDaemon]; got != gatewayVersion {
		t.Errorf("running[%q] = %q, want gatewayVersion = %q", releaseupdate.ComponentDaemon, got, gatewayVersion)
	}
	if _, ok := running[releaseupdate.ComponentAgent]; ok {
		t.Errorf("running vouches for %q, which this process cannot verify: %#v",
			releaseupdate.ComponentAgent, running)
	}
}

// TestDaemonRunningVersionsIncludesObservedAgent: once a task response has
// actually self-reported an agent version (daemon.AgentStatus.Observe), that
// -- not archied's release pipeline's expectation -- is ground truth for
// what is running, so it belongs in the verification set.
func TestDaemonRunningVersionsIncludesObservedAgent(t *testing.T) {
	var status daemon.AgentStatus
	status.Observe("1.9.11", "container")

	running := daemonRunningVersions(&status)

	if got := running[releaseupdate.ComponentAgent]; got != "1.9.11" {
		t.Errorf("running[%q] = %q, want the observed version %q", releaseupdate.ComponentAgent, got, "1.9.11")
	}
}
