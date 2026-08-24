package main

import (
	"os"
	"strings"
	"testing"
)

// Autonomous execution has one production composition: managed task
// containers over embedded or external NATS. These old switches must not
// quietly restore a host or no-broker path in the composition root.
func TestCompositionContainsNoAutonomousExecutionOptOut(t *testing.T) {
	for _, file := range []string{"bootstrap.go", "main.go"} {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"NATSModeOff", "Containers.Enabled"} {
			if strings.Contains(string(source), forbidden) {
				t.Errorf("%s contains removed autonomous execution switch %q", file, forbidden)
			}
		}
	}
}
