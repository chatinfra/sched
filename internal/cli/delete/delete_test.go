package delete_test

import (
	"os"
	"strings"
	"testing"

	"github.com/chatinfra/sched/internal/cli/clitest"
)

func TestDeleteExistingJobAndReconcilesArtifacts(t *testing.T) {
	h := clitest.New(t)
	h.PutJob(t, clitest.SampleJob())

	if _, err := os.Stat(h.JobPath("sched-1")); err != nil {
		t.Fatalf("job file missing before delete: %v", err)
	}
	if got := managedUnitCount(t, h.SystemdDir); got != 2 {
		t.Fatalf("managed units before delete = %d, want 2", got)
	}

	result := h.Run(t, "delete", "sched-1").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, result, "delete.schema.yaml")
	summary := clitest.DecodeYAMLAs[clitest.SummaryResponse](t, result.Stdout)
	if summary.Summary != "deleted sched-1" {
		t.Fatalf("delete summary = %#v", summary)
	}
	for _, omitted := range []string{"schemaVersion", "tenantId", "opencodeId", "commandId", "title", "createdAt", "updatedAt"} {
		if strings.Contains(result.Stdout, omitted+":") {
			t.Fatalf("human delete output includes verbose field %q:\n%s", omitted, result.Stdout)
		}
	}
	if _, err := os.Stat(h.JobPath("sched-1")); !os.IsNotExist(err) {
		t.Fatalf("job file still exists or stat failed: %v", err)
	}
	if got := managedUnitCount(t, h.SystemdDir); got != 0 {
		t.Fatalf("managed units after delete = %d, want 0", got)
	}
}

func TestDeleteFullYAMLOutput(t *testing.T) {
	h := clitest.New(t)
	h.PutJob(t, clitest.SampleJob())

	result := h.Run(t, "delete", "sched-1", "--full").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, result, "delete-full.schema.yaml")
	payload := clitest.DecodeYAMLAs[struct {
		ScheduleID string `json:"scheduleId"`
		Deleted    bool   `json:"deleted"`
	}](t, result.Stdout)
	if payload.ScheduleID != "sched-1" || !payload.Deleted {
		t.Fatalf("delete full payload = %#v", payload)
	}
}

func TestDeleteMissingAndInvalidScheduleID(t *testing.T) {
	h := clitest.New(t)

	missing := h.Run(t, "delete", "missing").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, missing, "delete.schema.yaml")
	summary := clitest.DecodeYAMLAs[clitest.SummaryResponse](t, missing.Stdout)
	if !strings.Contains(summary.Summary, "not found missing") {
		t.Fatalf("missing delete summary = %#v", summary)
	}

	invalid := h.Run(t, "delete", "bad/id").RequireError(t)
	clitest.RequireYAMLError(t, invalid, "error.schema.yaml")
	envelope := clitest.DecodeYAMLAs[clitest.ErrorEnvelope](t, invalid.Stderr)
	if envelope.Error.Code != "invalid_schedule_id" {
		t.Fatalf("invalid ID envelope = %#v", envelope.Error)
	}
}

func managedUnitCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", dir, err)
	}
	count := 0
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "sched-command-") && (strings.HasSuffix(name, ".service") || strings.HasSuffix(name, ".timer")) {
			count++
		}
	}
	return count
}
