package postgres_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

// A retry that resumes starts from the results its After stage recorded in
// its last run, and is refused when that run did not move past the stage.
func TestRetryTaskResume(t *testing.T) {
	cases := []struct {
		name    string
		resume  task.Resume
		want    string
		wantErr error
	}{
		{name: "after a completed stage", resume: task.Resume{From: "check", After: "analyse"}, want: `{"analyse":{"summary":"found it"}}`},
		{name: "from the first stage", resume: task.Resume{From: "analyse"}, want: `{}`},
		{name: "after a stage that failed", resume: task.Resume{From: "publish", After: "check"}, wantErr: storecontract.ErrResumeIncomplete},
		{name: "after a stage that never ran", resume: task.Resume{From: "publish", After: "nowhere"}, wantErr: storecontract.ErrResumeIncomplete},
		{name: "plain retry", want: `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			db := pgstore.Open(t)
			if _, err := db.EnqueueChatTask(ctx, "", "", "resume", "", "tdd", "", "", nil); err != nil {
				t.Fatal(err)
			}
			run, err := db.ClaimNext(ctx)
			if err != nil || run == nil {
				t.Fatalf("claim: %v", err)
			}
			finish := func(name string, to taskstate.StepStatus, results string) {
				id, _, err := db.StartStep(ctx, task.StepStart{ExecutionID: run.ID, Attempt: run.Attempt, Kind: task.StepKindStage, Name: name})
				if err != nil {
					t.Fatal(err)
				}
				var raw []byte
				if results != "" {
					raw = []byte(results)
				}
				if _, err := db.FinishStep(ctx, task.StepFinish{StepID: id, ExecutionID: run.ID, From: taskstate.StepRunning, To: to, Results: raw}); err != nil {
					t.Fatal(err)
				}
			}
			finish("analyse", taskstate.StepSucceeded, `{"analyse":{"summary":"found it"}}`)
			finish("check", taskstate.StepFailed, "")
			if err := db.ParkTask(ctx, run.ID, taskstate.Running, "stage check failed", taskstate.ParkNeedsHuman); err != nil {
				t.Fatal(err)
			}

			err = db.RetryTask(ctx, run.ID, taskstate.Parked, "", string(taskstate.RetryRefreshOntoBase), tc.resume)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("RetryTask error = %v, want %v", err, tc.wantErr)
			}
			got, err := db.TaskByID(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantErr != nil {
				if got.Status != taskstate.Parked {
					t.Fatalf("refused retry left the task %s, want parked", got.Status)
				}
				return
			}
			if got.ResumeFrom != tc.resume.From || !sameJSON(t, got.ResumeResults, tc.want) {
				t.Fatalf("resume = %q %s, want %q %s", got.ResumeFrom, got.ResumeResults, tc.resume.From, tc.want)
			}
		})
	}
}

func sameJSON(t *testing.T, got []byte, want string) bool {
	t.Helper()
	var a, b any
	if json.Unmarshal(got, &a) != nil || json.Unmarshal([]byte(want), &b) != nil {
		return false
	}
	ga, _ := json.Marshal(a)
	gb, _ := json.Marshal(b)
	return string(ga) == string(gb)
}
