package list

import (
	"reflect"
	"testing"

	"github.com/chatinfra/sched/internal/cli/clitest"
	"github.com/chatinfra/sched/internal/sched"
)

func TestListEmptyStateYAML(t *testing.T) {
	h := clitest.New(t)

	result := h.Run(t, "list").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, result, "list.schema.yaml")
	payload := decodeList(t, result.Stdout)
	if len(payload.Jobs) != 0 {
		t.Fatalf("empty YAML jobs = %#v", payload.Jobs)
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
	ids := make([]string, 0, len(payload.Jobs))
	for _, job := range payload.Jobs {
		ids = append(ids, job.ScheduleID)
	}
	if !reflect.DeepEqual(ids, []string{"sched-1", "sched-2"}) {
		t.Fatalf("list IDs = %#v", ids)
	}
}

func decodeList(t *testing.T, raw string) struct {
	Jobs []sched.Job `json:"jobs"`
} {
	t.Helper()
	return clitest.DecodeYAMLAs[struct {
		Jobs []sched.Job `json:"jobs"`
	}](t, raw)
}
