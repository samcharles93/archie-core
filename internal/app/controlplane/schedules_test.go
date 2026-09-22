package controlplane

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/scheduling"
)

func TestScheduleDefinitionComputesFirstRunAndRejectsDuplicateIDs(t *testing.T) {
	t.Parallel()

	definition := scheduleDefinition()
	jobs := []scheduling.JobSpec{{ID: "daily", Kind: scheduling.KindWorkflow, Schedule: scheduling.Schedule{Kind: scheduling.ScheduleInterval, Interval: scheduling.Duration(time.Hour)}}}
	input, err := json.Marshal(jobs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := definition.Decode(input)
	if err != nil {
		t.Fatal(err)
	}
	var normalized []scheduling.JobSpec
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
