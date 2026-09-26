package kitrun

import (
	"slices"
	"testing"
)

func TestWorkerEnvReachesNATSAndTheStateStoreThroughTheRelay(t *testing.T) {
	got, err := WorkerEnv(Endpoints{
		NATS: "nats://172.17.0.1:4222", NATSToken: "nt",
		StateStore: "10.0.0.5:7443", StateStoreToken: "st",
	}, 1001, 1002)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"NATS_URL=nats://archie-egress:4222", "NATS_TOKEN=nt",
		"STATE_STORE_URL=archie-egress:50051", "STATE_STORE_TOKEN=st",
		"WORKTREE_UID=1001", "WORKTREE_GID=1002",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("WorkerEnv = %q\nwant %q", got, want)
	}
}

func TestWorkerEnvWithoutAStateStoreOmitsIt(t *testing.T) {
	got, err := WorkerEnv(Endpoints{NATS: "tls://nats.internal:4222"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"NATS_URL=tls://archie-egress:4222", "WORKTREE_UID=0", "WORKTREE_GID=0"}) {
		t.Fatalf("WorkerEnv = %q", got)
	}
}

func TestRelayTargets(t *testing.T) {
	nats, store, err := relayTargets(Endpoints{NATS: "nats://172.17.0.1:4222", StateStore: "dns:///store.lan:7443"})
	if err != nil || nats != "172.17.0.1:4222" || store != "store.lan:7443" {
		t.Fatalf("relayTargets = %q, %q, %v", nats, store, err)
	}
	if _, _, err := relayTargets(Endpoints{NATS: "nats://a:1,nats://b:2"}); err == nil {
		t.Fatal("a NATS cluster URL list was accepted; the relay forwards one address")
	}
}
