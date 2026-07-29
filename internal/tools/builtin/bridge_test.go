package builtin

import (
	"testing"
)

func TestNonInteractiveBridgeLog(t *testing.T) {
	var bridge UIBridge = NonInteractiveBridge{}
	// This will fail to compile until Log is added to UIBridge and NonInteractiveBridge
	bridge.Log("test chunk")
}
