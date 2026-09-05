package drain

import "testing"

func TestDecideTable(t *testing.T) {
	bootA := Epoch{BootID: "boot-a", PID1Start: 100}
	bootB := Epoch{BootID: "boot-b", PID1Start: 100}

	tests := []struct {
		name    string
		current Epoch
		marker  *Marker
		want    Decision
	}{
		{
			name:    "no marker reports none",
			current: bootA,
			marker:  nil,
			want:    DecisionNone,
		},
		{
			name:    "marker matching current epoch is valid",
			current: bootA,
			marker:  &Marker{InstantiationEpoch: bootA},
			want:    DecisionValid,
		},
		{
			name:    "marker written against a prior boot is stale",
			current: bootA,
			marker:  &Marker{InstantiationEpoch: bootB},
			want:    DecisionStale,
		},
		{
			name:    "marker written against a prior pid1 start within same boot is stale",
			current: bootA,
			marker:  &Marker{InstantiationEpoch: Epoch{BootID: "boot-a", PID1Start: 99}},
			want:    DecisionStale,
		},
		{
			name:    "marker still valid when operator fields present",
			current: bootA,
			marker:  &Marker{InstantiationEpoch: bootA, Reason: "maintenance", RequestedAt: "2026-09-05T00:00:00Z"},
			want:    DecisionValid,
		},
		{
			name:    "empty current epoch fails closed to stale even on matching marker",
			current: Epoch{},
			marker:  &Marker{InstantiationEpoch: bootA},
			want:    DecisionStale,
		},
		{
			name:    "zero pid1 start in current epoch fails closed to stale",
			current: Epoch{BootID: "boot-a", PID1Start: 0},
			marker:  &Marker{InstantiationEpoch: Epoch{BootID: "boot-a", PID1Start: 0}},
			want:    DecisionStale,
		},
		{
			name:    "empty boot id in current epoch fails closed to stale",
			current: Epoch{BootID: "", PID1Start: 100},
			marker:  &Marker{InstantiationEpoch: Epoch{BootID: "", PID1Start: 100}},
			want:    DecisionStale,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Decide(test.current, test.marker); got != test.want {
				t.Fatalf("Decide() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestEpochEqual(t *testing.T) {
	if !(Epoch{BootID: "b", PID1Start: 1}).Equal(Epoch{BootID: "b", PID1Start: 1}) {
		t.Fatal("equal epochs reported unequal")
	}
	if (Epoch{BootID: "b", PID1Start: 1}).Equal(Epoch{BootID: "b", PID1Start: 2}) {
		t.Fatal("epochs differing only in pid1 start reported equal")
	}
	if (Epoch{BootID: "a", PID1Start: 1}).Equal(Epoch{BootID: "b", PID1Start: 1}) {
		t.Fatal("epochs differing only in boot id reported equal")
	}
}

func TestEpochEmpty(t *testing.T) {
	if !(Epoch{}).Empty() {
		t.Fatal("zero epoch should be empty")
	}
	if !(Epoch{BootID: "a", PID1Start: 0}).Empty() {
		t.Fatal("zero pid1 start should be empty")
	}
	if !(Epoch{BootID: "", PID1Start: 5}).Empty() {
		t.Fatal("empty boot id should be empty")
	}
	if (Epoch{BootID: "a", PID1Start: 5}).Empty() {
		t.Fatal("well-formed epoch should not be empty")
	}
}
