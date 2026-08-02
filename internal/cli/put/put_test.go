package put

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/chatinfra/sched/internal/cli/clitest"
	"github.com/chatinfra/sched/internal/sched"
)

func TestPutValidStdinPersistsJob(t *testing.T) {
	h := clitest.New(t)

	result := h.RunWithStdin(t, clitest.SampleJobJSON(t), "put", "--stdin").RequireSuccess(t)
	stdout := clitest.RequireTextStdout(t, result)
	for _, want := range []string{"Upserted schedule sched-1", "Status:", "active", "Schedule: cron 0 9 * * * UTC", "Command:  Daily-Backup", "Agent:    build-agent", "Workdir:  /data/opencode/work"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("put terminal output missing %q:\n%s", want, stdout)
		}
	}
	for _, omitted := range []string{"schemaVersion", "tenantId", "opencodeId", "commandId", "title", "createdAt", "updatedAt"} {
		if strings.Contains(stdout, omitted+":") {
			t.Fatalf("terminal put output includes verbose field %q:\n%s", omitted, stdout)
		}
	}
	if strings.Contains(stdout, "summary:") {
		t.Fatalf("terminal put output contains YAML summary wrapper:\n%s", stdout)
	}
	if result.Stderr != "" {
		t.Fatalf("put stderr = %q", result.Stderr)
	}
	full := h.Run(t, "get", "sched-1", "--full").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, full, "get-full.schema.yaml")
	job := clitest.DecodeYAMLAs[sched.Job](t, full.Stdout)
	if job.ScheduleID != "sched-1" || job.Title != "ci-command:cmd-1:sched-1" {
		t.Fatalf("put full job = %#v", job)
	}
	if _, err := os.Stat(h.JobPath("sched-1")); err != nil {
		t.Fatalf("persisted job missing: %v", err)
	}
}

func TestPutInvalidJSONMissingStdinAndValidation(t *testing.T) {
	h := clitest.New(t)

	invalidJSON := h.RunWithStdin(t, `{`, "put", "--stdin").RequireError(t)
	assertErrorCode(t, invalidJSON, "invalid_json")

	missingStdin := h.Run(t, "put").RequireError(t)
	assertErrorCode(t, missingStdin, "internal_error")
	if !strings.Contains(clitest.DecodeYAMLAs[clitest.ErrorEnvelope](t, missingStdin.Stderr).Error.Message, "usage: sched put --stdin") {
		t.Fatalf("missing --stdin envelope = %s", missingStdin.Stderr)
	}

	missingFieldJob := clitest.SampleJob()
	missingFieldJob.TenantID = ""
	validation := h.RunWithStdin(t, clitest.JobJSON(t, missingFieldJob), "put", "--stdin").RequireError(t)
	assertErrorCode(t, validation, "invalid_job")
}

func TestPutIdempotentUpdatePreservesCreatedAt(t *testing.T) {
	h := clitest.New(t)

	first := h.PutJob(t, clitest.SampleJob())
	updatedInput := clitest.SampleJob(clitest.WithCommandName("Deploy-Production"))
	updated := h.PutJob(t, updatedInput)

	if updated.CommandName != "Deploy-Production" {
		t.Fatalf("updated commandName = %q", updated.CommandName)
	}
	if !updated.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("CreatedAt changed: first=%s updated=%s", first.CreatedAt, updated.CreatedAt)
	}

	listResult := h.Run(t, "list").RequireSuccess(t)
	stdout := clitest.RequireTextStdout(t, listResult)
	if !strings.Contains(stdout, "Deploy-Production @ build-agent") {
		t.Fatalf("list after update = %s", stdout)
	}
}

func TestPutManyPersistsOrderedJobsAndReconcilesCompleteSet(t *testing.T) {
	h := clitest.New(t)
	first := clitest.SampleJob()
	second := clitest.SampleJob(clitest.WithScheduleID("sched-2"), clitest.WithCommandName("Weekly-Report"))
	input, err := json.Marshal([]sched.Job{first, second})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	result := h.RunWithStdin(t, string(input), "put-many", "--stdin", "--full").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, result, "put-many-full.schema.yaml")
	response := clitest.DecodeYAMLAs[struct {
		Jobs []sched.Job `json:"jobs"`
	}](t, result.Stdout)
	if len(response.Jobs) != 2 || response.Jobs[0].ScheduleID != "sched-1" || response.Jobs[1].ScheduleID != "sched-2" {
		t.Fatalf("put-many jobs = %#v", response.Jobs)
	}
	for _, scheduleID := range []string{"sched-1", "sched-2"} {
		if _, err := os.Stat(h.JobPath(scheduleID)); err != nil {
			t.Fatalf("persisted job %s missing: %v", scheduleID, err)
		}
	}
	units, err := os.ReadDir(h.SystemdDir)
	if err != nil {
		t.Fatalf("ReadDir(systemd) error = %v", err)
	}
	if len(units) != 4 {
		t.Fatalf("reconciled unit count = %d, want 4 for the complete two-job set", len(units))
	}
}

func TestPutManyRejectsEmptyInputAndPreservesEarlierWriteOnLaterValidationFailure(t *testing.T) {
	h := clitest.New(t)
	empty := h.RunWithStdin(t, `[]`, "put-many", "--stdin", "--full").RequireError(t)
	assertErrorCode(t, empty, "invalid_job")

	invalidSecond := clitest.SampleJob(clitest.WithScheduleID("sched-2"))
	invalidSecond.TenantID = ""
	input, err := json.Marshal([]sched.Job{clitest.SampleJob(), invalidSecond})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	failed := h.RunWithStdin(t, string(input), "put-many", "--stdin", "--full").RequireError(t)
	assertErrorCode(t, failed, "invalid_job")
	if _, err := os.Stat(h.JobPath("sched-1")); err != nil {
		t.Fatalf("first ordered write missing after second validation failure: %v", err)
	}
	if _, err := os.Stat(h.JobPath("sched-2")); !os.IsNotExist(err) {
		t.Fatalf("invalid second write exists or stat failed unexpectedly: %v", err)
	}
}

func assertErrorCode(t *testing.T, result clitest.Result, code string) {
	t.Helper()
	clitest.RequireYAMLError(t, result, "error.schema.yaml")
	envelope := clitest.DecodeYAMLAs[clitest.ErrorEnvelope](t, result.Stderr)
	if envelope.Error.Code != code || envelope.Error.Message == "" {
		t.Fatalf("error envelope = %#v, want code %q", envelope.Error, code)
	}
}
