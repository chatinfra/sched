package list

import (
	"strings"
	"testing"

	"github.com/chatinfra/sched/internal/cli/clitest"
	"github.com/chatinfra/sched/internal/sched"
)

func TestListEmptyStateTerminalText(t *testing.T) {
	h := clitest.New(t)

	result := h.Run(t, "list").RequireSuccess(t)
	stdout := clitest.RequireTextStdout(t, result)
	if strings.TrimSpace(stdout) != "No schedules found." {
		t.Fatalf("empty terminal list output = %q", stdout)
	}
	if strings.Contains(stdout, "jobs:") {
		t.Fatalf("empty terminal list output contains YAML wrapper:\n%s", stdout)
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
	firstText := clitest.RequireTextStdout(t, first)
	second := h.Run(t, "list").RequireSuccess(t)
	secondText := clitest.RequireTextStdout(t, second)
	if first.Stdout != second.Stdout {
		t.Fatalf("list terminal output changed between calls:\nfirst=%s\nsecond=%s", first.Stdout, second.Stdout)
	}
	lines := strings.Split(strings.TrimSuffix(firstText, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("list terminal table lines = %#v", lines)
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
		if strings.Contains(firstText, omitted+":") {
			t.Fatalf("terminal list output includes verbose field %q:\n%s", omitted, firstText)
		}
	}
	if strings.Contains(firstText, "jobs:") || strings.Contains(firstText, "|-") || strings.Contains(firstText, "{id:") || strings.Contains(firstText, "- {") {
		t.Fatalf("list output contains YAML wrapper or flow-style mapping:\n%s", firstText)
	}
	if secondText == "" {
		t.Fatalf("second terminal output empty")
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
