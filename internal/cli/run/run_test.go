package run

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chatinfra/sched/internal/cli/clitest"
	"github.com/chatinfra/sched/internal/sched"
)

func TestRunManualAndScheduledSourcesRecordHistory(t *testing.T) {
	h := clitest.New(t)
	h.PutJob(t, clitest.SampleJob())
	opencode := h.FakeExecutable(t, "opencode-success", "printf 'args=%s\\n' \"$*\"\n")

	manual := h.Run(t, "--opencode-bin", opencode, "run", "sched-1", "--source", "manual").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, manual, "run.schema.yaml")
	manualSummary := clitest.DecodeYAMLAs[clitest.RunSummary](t, manual.Stdout)
	assertRunSummary(t, manualSummary, sched.RunSourceManual, sched.RunStatusSuccess)
	for _, omitted := range []string{"logPath", "commandLine"} {
		if strings.Contains(manual.Stdout, omitted+":") {
			t.Fatalf("compact run output includes verbose field %q:\n%s", omitted, manual.Stdout)
		}
	}
	manualRecord := latestRunRecord(t, h, "sched-1")
	assertLogContains(t, manualRecord.LogPath, "--title ci-command:cmd-1:sched-1")

	scheduled := h.Run(t, "--opencode-bin", opencode, "run", "sched-1", "--source", "scheduled", "--full").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, scheduled, "run-full.schema.yaml")
	scheduledRecord := clitest.DecodeYAMLAs[sched.RunRecord](t, scheduled.Stdout)
	assertRunRecord(t, scheduledRecord, sched.RunSourceScheduled, sched.RunStatusSuccess)

	jobResult := h.Run(t, "get", "sched-1", "--full").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, jobResult, "get-full.schema.yaml")
	job := clitest.DecodeYAMLAs[sched.Job](t, jobResult.Stdout)
	if job.LastRunStatus != sched.RunStatusSuccess || job.LastRunAt == nil {
		t.Fatalf("job run metadata = %#v", job)
	}

	history := h.Run(t, "history", "--schedule-id", "sched-1").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, history, "history.schema.yaml")
	var payload struct {
		Runs []clitest.RunSummary `json:"runs"`
	}
	payload = clitest.DecodeYAMLAs[struct {
		Runs []clitest.RunSummary `json:"runs"`
	}](t, history.Stdout)
	if len(payload.Runs) != 2 {
		t.Fatalf("history runs = %#v", payload.Runs)
	}
	if strings.Contains(history.Stdout, "logPath:") || strings.Contains(history.Stdout, "commandLine:") {
		t.Fatalf("compact history includes verbose run fields:\n%s", history.Stdout)
	}
}

func TestRunInvalidSourceAndFailingOpenCode(t *testing.T) {
	h := clitest.New(t)
	h.PutJob(t, clitest.SampleJob())

	invalid := h.Run(t, "run", "sched-1", "--source", "cron").RequireError(t)
	clitest.RequireYAMLError(t, invalid, "error.schema.yaml")
	envelope := clitest.DecodeYAMLAs[clitest.ErrorEnvelope](t, invalid.Stderr)
	if envelope.Error.Code != "invalid_run_source" {
		t.Fatalf("invalid source envelope = %#v", envelope.Error)
	}

	failing := h.FakeExecutable(t, "opencode-fail", "exit 7\n")
	result := h.Run(t, "--opencode-bin", failing, "run", "sched-1", "--source", "manual").RequireError(t)
	clitest.RequireYAMLError(t, result, "error.schema.yaml")
	errEnvelope := clitest.DecodeYAMLAs[clitest.ErrorEnvelope](t, result.Stderr)
	record := latestRunRecord(t, h, "sched-1")
	if record.Status != sched.RunStatusFailed || record.ExitCode == nil || *record.ExitCode != 7 {
		t.Fatalf("failing run record = %#v", record)
	}
	if errEnvelope.Error.Code != "run_failed" {
		t.Fatalf("failing run envelope = %#v", errEnvelope.Error)
	}
}

func TestRunOverlappingRunSkipsWithoutInvokingOpenCode(t *testing.T) {
	h := clitest.New(t)
	h.PutJob(t, clitest.SampleJob())
	opencode := h.FakeExecutable(t, "opencode-should-not-run", "exit 42\n")

	lock := map[string]any{"scheduleId": "sched-1", "pid": os.Getpid(), "startedAt": time.Now().UTC()}
	data, err := json.Marshal(lock)
	if err != nil {
		t.Fatalf("Marshal(lock) error = %v", err)
	}
	if err := os.WriteFile(h.LockPath("sched-1"), data, 0o644); err != nil {
		t.Fatalf("WriteFile(lock) error = %v", err)
	}

	result := h.Run(t, "--opencode-bin", opencode, "run", "sched-1", "--source", "scheduled").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, result, "run.schema.yaml")
	record := clitest.DecodeYAMLAs[clitest.RunSummary](t, result.Stdout)
	if record.Status != sched.RunStatusSkipped || record.Source != sched.RunSourceScheduled || !strings.Contains(record.Message, "already running") {
		t.Fatalf("skipped record = %#v", record)
	}
}

func TestRunSuppressedJobReturnsSkippedRecordAndError(t *testing.T) {
	h := clitest.New(t)
	h.PutJob(t, clitest.SampleJob(clitest.WithTimerSuppressed("agent_disabled")))
	opencode := h.FakeExecutable(t, "opencode-should-not-run-suppressed", "exit 42\n")

	result := h.Run(t, "--opencode-bin", opencode, "run", "sched-1", "--source", "manual").RequireError(t)
	clitest.RequireYAMLError(t, result, "error.schema.yaml")
	envelope := clitest.DecodeYAMLAs[clitest.ErrorEnvelope](t, result.Stderr)
	record := latestRunRecord(t, h, "sched-1")
	if record.Status != sched.RunStatusSkipped || !strings.Contains(record.Message, "agent_disabled") {
		t.Fatalf("suppressed run record = %#v", record)
	}
	if envelope.Error.Code != "job_suppressed" {
		t.Fatalf("suppressed run envelope = %#v", envelope.Error)
	}
}

func assertRunRecord(t *testing.T, record sched.RunRecord, source string, status string) {
	t.Helper()
	if record.ScheduleID != "sched-1" || record.Source != source || record.Status != status || record.RunID == "" {
		t.Fatalf("run record = %#v", record)
	}
	if record.ExitCode == nil || *record.ExitCode != 0 {
		t.Fatalf("run exit code = %#v", record.ExitCode)
	}
}

func assertRunSummary(t *testing.T, record clitest.RunSummary, source string, status string) {
	t.Helper()
	if record.ScheduleID != "sched-1" || record.Source != source || record.Status != status || record.ID == "" || record.StartedAt == "" {
		t.Fatalf("run summary = %#v", record)
	}
	if record.ExitCode == nil || *record.ExitCode != 0 {
		t.Fatalf("run summary exit code = %#v", record.ExitCode)
	}
}

func assertLogContains(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("log missing %q:\n%s", want, string(data))
	}
}

func latestRunRecord(t *testing.T, h *clitest.Harness, scheduleID string) sched.RunRecord {
	t.Helper()
	history := h.Run(t, "history", "--schedule-id", scheduleID, "--full").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, history, "history-full.schema.yaml")
	payload := clitest.DecodeYAMLAs[struct {
		Runs []sched.RunRecord `json:"runs"`
	}](t, history.Stdout)
	if len(payload.Runs) == 0 {
		t.Fatalf("no run history for %s", scheduleID)
	}
	return payload.Runs[0]
}
