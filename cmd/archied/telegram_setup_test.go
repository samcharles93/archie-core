package main

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/installtype"
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
