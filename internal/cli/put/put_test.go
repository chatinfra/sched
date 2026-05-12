package put

import (
	"os"
	"strings"
	"testing"

	"github.com/chatinfra/sched/internal/cli/clitest"
	"github.com/chatinfra/sched/internal/sched"
)

func TestPutValidStdinPersistsJob(t *testing.T) {
	h := clitest.New(t)

	result := h.RunWithStdin(t, clitest.SampleJobJSON(t), "put", "--stdin").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, result, "put.schema.yaml")
	summary := clitest.DecodeYAMLAs[clitest.SummaryResponse](t, result.Stdout)
	for _, want := range []string{"upserted", "sched-1", sched.StatusActive, "Daily-Backup @ build-agent", "workdir=/data/opencode/work"} {
		if !strings.Contains(summary.Summary, want) {
			t.Fatalf("put summary %q missing %q", summary.Summary, want)
		}
	}
	for _, omitted := range []string{"schemaVersion", "tenantId", "opencodeId", "commandId", "title", "createdAt", "updatedAt"} {
		if strings.Contains(result.Stdout, omitted+":") {
			t.Fatalf("human put output includes verbose field %q:\n%s", omitted, result.Stdout)
		}
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
	clitest.RequireYAMLStdout(t, listResult, "list.schema.yaml")
	payload := clitest.DecodeYAMLAs[clitest.ListResponse](t, listResult.Stdout)
	if !strings.Contains(payload.Jobs, "Deploy-Production @ build-agent") {
		t.Fatalf("list after update = %#v", payload.Jobs)
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
