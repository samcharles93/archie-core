package controlplane

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/infrastructure/cronstore"
)

func TestScheduleDefinitionComputesFirstRunAndRejectsDuplicateIDs(t *testing.T) {
	t.Parallel()

	definition := scheduleDefinition()
	jobs := []cronstore.JobSpec{{ID: "daily", Kind: cronstore.KindWorkflow, Schedule: cronstore.Schedule{Kind: cronstore.ScheduleInterval, Interval: cronstore.Duration(time.Hour)}}}
	input, err := json.Marshal(jobs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := definition.Decode(input)
	if err != nil {
		t.Fatal(err)
	}
	var normalized []cronstore.JobSpec
	if err := json.Unmarshal(got, &normalized); err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 1 || normalized[0].NextRun.IsZero() || normalized[0].Created.IsZero() || normalized[0].Updated.IsZero() {
		t.Fatalf("normalized schedules = %+v", normalized)
	}
	duplicate, err := json.Marshal(append(jobs, jobs[0]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := definition.Decode(duplicate); err == nil {
		t.Fatal("duplicate schedule IDs accepted")
	}
}
