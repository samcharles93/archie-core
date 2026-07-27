package main

import (
	"os"
	"strings"
	"testing"
)

func TestReleaseMetadataIsBuiltAndPackagedWithArchied(t *testing.T) {
	dockerfile, err := os.ReadFile("../../Dockerfile.archied")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		"ARG GATEWAY_VERSION=dev",
		"ARG RUNTIME_VERSION=dev",
		"-X main.gatewayVersion=${GATEWAY_VERSION}",
		"-X main.runtimeVersion=${RUNTIME_VERSION}",
		"COPY CHANGELOG.archied.md /usr/share/archie/CHANGELOG.archied.md",
		"COPY CHANGELOG.archie.md /usr/share/archie/CHANGELOG.archie.md",
	} {
		if !strings.Contains(string(dockerfile), marker) {
			t.Errorf("Dockerfile.archied missing %q", marker)
		}
	}

	workflow, err := os.ReadFile("../../.gitea/workflows/deploy.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		"fetch-depth: 0",
		`--points-at HEAD --list 'archied/v*'`,
		`--points-at HEAD --list 'archie/v*'`,
		`--build-arg GATEWAY_VERSION="$GATEWAY_VERSION"`,
		`--build-arg RUNTIME_VERSION="$RUNTIME_VERSION"`,
	} {
		if !strings.Contains(string(workflow), marker) {
			t.Errorf("deploy workflow missing %q", marker)
		}
	}
}

func TestChangelogsTrackGatewayAndRuntimeIndependently(t *testing.T) {
	changelog, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	index := string(changelog)
	for _, marker := range []string{
		"CHANGELOG.archied.md",
		"CHANGELOG.archie.md",
		"archied/vX.Y.Z",
		"archie/vX.Y.Z",
	} {
		if !strings.Contains(index, marker) {
			t.Errorf("CHANGELOG.md missing %q", marker)
		}
	}
	gateway, err := os.ReadFile("../../CHANGELOG.archied.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"## [1.1.0] - 2026-07-27", "/help", "/provider", "/model", "MCP"} {
		if !strings.Contains(string(gateway), marker) {
			t.Errorf("CHANGELOG.archied.md missing %q", marker)
		}
	}
	runtime, err := os.ReadFile("../../CHANGELOG.archie.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runtime), "## [1.1.0] - 2026-07-27") {
		t.Error("CHANGELOG.archie.md missing current release section")
	}
}

func TestTelegramReleaseAnnouncementsAreWiredFromDeploymentMetadata(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, marker := range []string{
		"releaseannounce.Announcer{",
		`Label:         "THE GATEWAY"`,
		`Label:         "THE RUNTIME"`,
		"Version:       gatewayVersion",
		"Version:       runtimeVersion",
		"tg.ReleaseAnnouncements = releaseAnnouncements",
	} {
		if !strings.Contains(text, marker) {
			t.Errorf("Telegram release announcement wiring missing %q", marker)
		}
	}
}
