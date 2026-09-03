package configtemplate

import (
	"strings"
	"testing"
)

func TestDeployWorkflowOnlyPublishesRuntimeImageForRuntimeTag(t *testing.T) {
	source := readDeploymentFile(t, ".github/workflows/deploy.yml")

	// The fallback remains necessary because Dockerfile.archied accepts a
	// runtime version, but it must not be used as the image-publish selector.
	for _, required := range []string{
		`RUNTIME_TAG="$(git tag --points-at HEAD --list 'archie/v*' | sort -V | tail -1)"`,
		`echo "runtime_tag=$RUNTIME_TAG" >> "$GITHUB_OUTPUT"`,
		"if: steps.versions.outputs.runtime_tag != ''",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("deploy workflow is missing runtime-image guard %q", required)
		}
	}

	// The gateway build remains unconditional, while the runtime build is
	// guarded. This verifies both sides of the gateway-only/dual-tag contract.
	agentMarker := "      - name: Build and push archie-agent\n"
	agentStart := strings.Index(source, agentMarker)
	if agentStart < 0 {
		t.Fatal("deploy workflow has no archie-agent build step")
	}
	if !strings.Contains(source[agentStart:], agentMarker+"        if: steps.versions.outputs.runtime_tag != ''\n") {
		t.Fatal("archie-agent build step is not conditionally enabled")
	}

	gatewayMarker := "      - name: Build and push archied\n"
	gatewayStart := strings.Index(source, gatewayMarker)
	if gatewayStart < 0 {
		t.Fatal("deploy workflow has no archied build step")
	}
	if strings.Contains(source[gatewayStart:agentStart], "\n        if:") {
		t.Fatal("archied build step is unexpectedly conditional")
	}
}
