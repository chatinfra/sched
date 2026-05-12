package stop

import (
	"strings"
	"testing"

	"github.com/chatinfra/sched/internal/cli/clitest"
)

func TestStopCompactYAMLOutput(t *testing.T) {
	h := clitest.New(t)
	h.PutJob(t, clitest.SampleJob())

	result := h.Run(t, "stop", "sched-1").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, result, "stop.schema.yaml")
	payload := clitest.DecodeYAMLAs[struct {
		ScheduleID  string   `json:"scheduleId"`
		ServiceName string   `json:"serviceName"`
		Stopped     bool     `json:"stopped"`
		Message     string   `json:"message"`
		Warnings    []string `json:"warnings"`
	}](t, result.Stdout)
	if payload.ScheduleID != "sched-1" || payload.ServiceName == "" || payload.Stopped || !strings.Contains(payload.Message, "systemctl disabled") {
		t.Fatalf("stop payload = %#v", payload)
	}
	for _, omitted := range []string{"schemaVersion", "tenantId", "opencodeId", "commandId", "title", "createdAt", "updatedAt"} {
		if strings.Contains(result.Stdout, omitted+":") {
			t.Fatalf("compact stop output includes verbose field %q:\n%s", omitted, result.Stdout)
		}
	}
}

func TestStopMissingAndInvalidScheduleID(t *testing.T) {
	h := clitest.New(t)

	missing := h.Run(t, "stop", "missing").RequireError(t)
	assertErrorCode(t, missing, "job_not_found")

	invalid := h.Run(t, "stop", "bad/id").RequireError(t)
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
