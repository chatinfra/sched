package export

import (
	"testing"

	"github.com/chatinfra/sched/internal/cli/clitest"
	"github.com/chatinfra/sched/internal/sched"
)

func TestExportEmptyYAML(t *testing.T) {
	h := clitest.New(t)

	result := h.Run(t, "export").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, result, "export.schema.yaml")
	exported := clitest.DecodeYAMLAs[sched.Export](t, result.Stdout)
	if exported.SchemaVersion != sched.SchemaVersion || len(exported.Jobs) != 0 {
		t.Fatalf("empty export = %#v", exported)
	}

}

func TestExportPopulatedYAMLShape(t *testing.T) {
	h := clitest.New(t)
	h.PutJob(t, clitest.SampleJob(clitest.WithScheduleID("sched-1")))
	h.PutJob(t, clitest.SampleJob(
		clitest.WithScheduleID("sched-2"),
		clitest.WithOpenCodeID("opencode-2"),
		clitest.WithCommandID("cmd-2"),
		clitest.WithCommandName("Nightly-Report"),
	))

	result := h.Run(t, "export").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, result, "export.schema.yaml")
	exported := clitest.DecodeYAMLAs[sched.Export](t, result.Stdout)
	if exported.SchemaVersion != sched.SchemaVersion || len(exported.Jobs) != 2 {
		t.Fatalf("populated export = %#v", exported)
	}
	if exported.Jobs[0].ScheduleID != "sched-1" || exported.Jobs[1].ScheduleID != "sched-2" {
		t.Fatalf("export job order = %#v", exported.Jobs)
	}

}
