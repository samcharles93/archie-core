package controlplane

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/scheduling"
	"github.com/samcharles93/archie-core/internal/infrastructure/cronstore"
)

const SchedulesKind = "schedules"

func scheduleDefinition() Definition {
	return Definition{
		Kind:      SchedulesKind,
		Title:     "Schedules",
		Schema:    arraySchema,
		ApplyMode: "live",
		Seed:      func(config.Config) any { return []cronstore.JobSpec{} },
		Validate: func(input []byte) error {
			return validateAs(input, validateSchedules)
		},
		Normalize: normalizeSchedules,
	}
}

func validateSchedules(jobs []cronstore.JobSpec) error {
	seen := make(map[string]struct{}, len(jobs))
	for _, job := range jobs {
		if strings.TrimSpace(job.ID) == "" {
			return fmt.Errorf("schedule ID is required")
		}
		if _, exists := seen[job.ID]; exists {
			return fmt.Errorf("duplicate schedule %q", job.ID)
		}
		seen[job.ID] = struct{}{}
		if job.Kind != cronstore.KindWorkflow {
			return fmt.Errorf("schedule %q has unsupported kind %q", job.ID, job.Kind)
		}
		if err := job.Schedule.Validate(); err != nil {
			return err
		}
		if err := scheduling.Pool(job.Pool).Validate(); err != nil {
			return err
		}
	}
	return nil
}

func normalizeSchedules(input []byte) ([]byte, error) {
	var jobs []cronstore.JobSpec
	if err := json.Unmarshal(input, &jobs); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for i := range jobs {
		jobs[i].Schedule = jobs[i].Schedule.Resolved()
		if jobs[i].NextRun.IsZero() {
			next, err := jobs[i].Schedule.FirstRun(now)
			if err != nil {
				return nil, err
			}
			jobs[i].NextRun = next.UTC()
		}
		if jobs[i].Created.IsZero() {
			jobs[i].Created = now
		}
		jobs[i].Updated = now
	}
	return json.Marshal(jobs)
}
