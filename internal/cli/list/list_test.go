package list

import (
	"strings"
	"testing"

	"github.com/chatinfra/sched/internal/cli/clitest"
	"github.com/chatinfra/sched/internal/sched"
)

func TestListEmptyStateYAML(t *testing.T) {
	h := clitest.New(t)

	result := h.Run(t, "list").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, result, "list.schema.yaml")
	payload := decodeList(t, result.Stdout)
	if payload.Jobs != "none" {
		t.Fatalf("empty YAML jobs = %q", payload.Jobs)
	}
}

func TestListPopulatedStateShapeAndStableOutput(t *testing.T) {
	h := clitest.New(t)
	h.PutJob(t, clitest.SampleJob(clitest.WithScheduleID("sched-1")))
	h.PutJob(t, clitest.SampleJob(
		clitest.WithScheduleID("sched-2"),
		clitest.WithOpenCodeID("opencode-2"),
		clitest.WithCommandID("cmd-2"),
		clitest.WithCommandName("Nightly-Report"),
	))

	first := h.Run(t, "list").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, first, "list.schema.yaml")
	second := h.Run(t, "list").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, second, "list.schema.yaml")
	if first.Stdout != second.Stdout {
		t.Fatalf("list YAML output changed between calls:\nfirst=%s\nsecond=%s", first.Stdout, second.Stdout)
	}
	payload := decodeList(t, first.Stdout)
	lines := strings.Split(payload.Jobs, "\n")
	if len(lines) != 3 {
		t.Fatalf("list jobs table lines = %#v", lines)
	}
	if !strings.Contains(lines[0], "ID") || !strings.Contains(lines[0], "STATUS") || !strings.Contains(lines[0], "SCHEDULE") || !strings.Contains(lines[0], "TARGET") {
		t.Fatalf("list jobs header = %q", lines[0])
	}
	if !strings.Contains(lines[1], "sched-1") || !strings.Contains(lines[1], "active") || !strings.Contains(lines[1], "cron 0 9 * * * UTC") || !strings.Contains(lines[1], "Daily-Backup @ build-agent") {
		t.Fatalf("first list row = %q", lines[1])
	}
	if !strings.Contains(lines[2], "sched-2") || !strings.Contains(lines[2], "Nightly-Report @ build-agent") {
		t.Fatalf("second list row = %q", lines[2])
	}
	for _, omitted := range []string{"schemaVersion", "tenantId", "opencodeId", "commandId", "title", "createdAt", "updatedAt"} {
		if strings.Contains(first.Stdout, omitted+":") {
			t.Fatalf("human list output includes verbose field %q:\n%s", omitted, first.Stdout)
		}
	}
	if strings.Contains(first.Stdout, "{id:") || strings.Contains(first.Stdout, "- {") {
		t.Fatalf("list output contains flow-style mapping:\n%s", first.Stdout)
	}

	full := h.Run(t, "list", "--full").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, full, "list-full.schema.yaml")
	fullPayload := clitest.DecodeYAMLAs[struct {
		Jobs []sched.Job `json:"jobs"`
	}](t, full.Stdout)
	if len(fullPayload.Jobs) != 2 || fullPayload.Jobs[0].TenantID == "" || fullPayload.Jobs[0].Title == "" {
		t.Fatalf("full list output = %#v", fullPayload.Jobs)
	}
}

func decodeList(t *testing.T, raw string) clitest.ListResponse {
	t.Helper()
	return clitest.DecodeYAMLAs[clitest.ListResponse](t, raw)
}
