package get

import (
	"strings"
	"testing"

	"github.com/chatinfra/sched/internal/cli/clitest"
	"github.com/chatinfra/sched/internal/sched"
)

func TestGetFoundJobTerminalOutput(t *testing.T) {
	h := clitest.New(t)
	h.PutJob(t, clitest.SampleJob())

	result := h.Run(t, "get", "sched-1").RequireSuccess(t)
	stdout := clitest.RequireTextStdout(t, result)
	for _, want := range []string{"sched-1", "Status:", "active", "Command:  Daily-Backup", "Agent:    build-agent", "Schedule: cron 0 9 * * * UTC", "Workdir:  /data/opencode/work"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("get terminal output missing %q:\n%s", want, stdout)
		}
	}
	for _, omitted := range []string{"schemaVersion", "tenantId", "opencodeId", "commandId", "title", "createdAt", "updatedAt"} {
		if strings.Contains(stdout, omitted+":") {
			t.Fatalf("terminal get output includes verbose field %q:\n%s", omitted, stdout)
		}
	}
	if strings.Contains(stdout, "summary:") {
		t.Fatalf("terminal get output contains YAML summary wrapper:\n%s", stdout)
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
