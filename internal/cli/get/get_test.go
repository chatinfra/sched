package get

import (
	"strings"
	"testing"

	"github.com/chatinfra/sched/internal/cli/clitest"
	"github.com/chatinfra/sched/internal/sched"
)

func TestGetFoundJobYAMLOutput(t *testing.T) {
	h := clitest.New(t)
	h.PutJob(t, clitest.SampleJob())

	result := h.Run(t, "get", "sched-1").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, result, "get.schema.yaml")
	summary := clitest.DecodeYAMLAs[clitest.JobSummary](t, result.Stdout)
	if summary.ID != "sched-1" || summary.Target != "Daily-Backup @ build-agent" || summary.Schedule != "cron 0 9 * * * UTC" || summary.Workdir == "" {
		t.Fatalf("get YAML summary = %#v", summary)
	}
	for _, omitted := range []string{"schemaVersion", "tenantId", "opencodeId", "commandId", "title", "createdAt", "updatedAt"} {
		if strings.Contains(result.Stdout, omitted+":") {
			t.Fatalf("compact get output includes verbose field %q:\n%s", omitted, result.Stdout)
		}
	}

	full := h.Run(t, "get", "sched-1", "--full").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, full, "get-full.schema.yaml")
	job := clitest.DecodeYAMLAs[sched.Job](t, full.Stdout)
	if job.ScheduleExpression != "0 9 * * *" || job.Timezone != "UTC" || job.Title != "ci-command:cmd-1:sched-1" {
		t.Fatalf("get job details = %#v", job)
	}
}

func TestGetMissingAndInvalidScheduleID(t *testing.T) {
	h := clitest.New(t)

	missing := h.Run(t, "get", "missing").RequireError(t)
	assertErrorCode(t, missing, "job_not_found")

	invalid := h.Run(t, "get", "bad/id").RequireError(t)
	assertErrorCode(t, invalid, "invalid_schedule_id")
}

func assertErrorCode(t *testing.T, result clitest.Result, code string) {
	t.Helper()
	clitest.RequireYAMLError(t, result, "error.schema.yaml")
	envelope := clitest.DecodeYAMLAs[clitest.ErrorEnvelope](t, result.Stderr)
	if envelope.Error.Code != code || envelope.Error.Message == "" {
		t.Fatalf("error envelope = %#v, want code %q", envelope.Error, code)
	}
}
