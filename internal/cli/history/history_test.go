package history

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chatinfra/sched/internal/cli/clitest"
	"github.com/chatinfra/sched/internal/sched"
)

func TestHistoryTopLevelListsAllRunsNewestFirst(t *testing.T) {
	h := clitest.New(t)
	oldStarted := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	newStarted := oldStarted.Add(time.Minute)
	newFinished := newStarted.Add(2 * time.Second)
	exitCode := 7
	writeRunRecords(t, h.RunPath("sched-1"), sched.RunRecord{RunID: "old-run", ScheduleID: "sched-1", Source: sched.RunSourceManual, Status: sched.RunStatusSuccess, StartedAt: oldStarted})
	writeRunRecords(t, h.RunPath("sched-2"), sched.RunRecord{RunID: "new-run", ScheduleID: "sched-2", Source: sched.RunSourceScheduled, Status: sched.RunStatusFailed, StartedAt: newStarted, FinishedAt: &newFinished, DurationMs: 2000, ExitCode: &exitCode, LogPath: "/data/opencode/log/new.log", CommandLine: []string{"opencode", "run"}})

	all := h.Run(t, "history", "--limit", "1").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, all, "history.schema.yaml")
	allPayload := decodeHistory(t, all.Stdout)
	if allPayload.ScheduleID != "" {
		t.Fatalf("all history payload = %#v", allPayload)
	}
	runLines := strings.Split(allPayload.Runs, "\n")
	if len(runLines) != 2 {
		t.Fatalf("all history run table = %#v", runLines)
	}
	if !strings.Contains(runLines[0], "RUN") || !strings.Contains(runLines[0], "SCHEDULE") || !strings.Contains(runLines[0], "DURATION") || !strings.Contains(runLines[0], "EXIT") {
		t.Fatalf("history header = %q", runLines[0])
	}
	if !strings.Contains(runLines[1], "new-run") || !strings.Contains(runLines[1], "sched-2") || !strings.Contains(runLines[1], "scheduled") || !strings.Contains(runLines[1], "failed") || !strings.Contains(runLines[1], "2000ms") || !strings.Contains(runLines[1], "7") {
		t.Fatalf("history row = %q", runLines[1])
	}
	if strings.Contains(all.Stdout, "logPath:") || strings.Contains(all.Stdout, "commandLine:") {
		t.Fatalf("human history includes verbose metadata:\n%s", all.Stdout)
	}
	if strings.Contains(all.Stdout, "{id:") || strings.Contains(all.Stdout, "- {") {
		t.Fatalf("history output contains flow-style mapping:\n%s", all.Stdout)
	}

	full := h.Run(t, "history", "--limit", "1", "--full").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, full, "history-full.schema.yaml")
	fullPayload := clitest.DecodeYAMLAs[struct {
		Runs []sched.RunRecord `json:"runs"`
	}](t, full.Stdout)
	if len(fullPayload.Runs) != 1 || fullPayload.Runs[0].RunID != "new-run" || fullPayload.Runs[0].LogPath == "" || len(fullPayload.Runs[0].CommandLine) == 0 {
		t.Fatalf("full history payload = %#v", fullPayload)
	}

}

func TestHistoryTopLevelFiltersByScheduleID(t *testing.T) {
	h := clitest.New(t)
	started := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	writeRunRecords(t, h.RunPath("sched-1"), sched.RunRecord{RunID: "sched-1-run", ScheduleID: "sched-1", Source: sched.RunSourceManual, Status: sched.RunStatusSuccess, StartedAt: started})
	writeRunRecords(t, h.RunPath("sched-2"), sched.RunRecord{RunID: "sched-2-run", ScheduleID: "sched-2", Source: sched.RunSourceScheduled, Status: sched.RunStatusFailed, StartedAt: started.Add(time.Minute)})

	result := h.Run(t, "history", "--schedule-id", "sched-1").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, result, "history.schema.yaml")
	payload := decodeHistory(t, result.Stdout)
	if payload.ScheduleID != "sched-1" || !strings.Contains(payload.Runs, "sched-1-run") || strings.Contains(payload.Runs, "sched-2-run") {
		t.Fatalf("filtered history payload = %#v", payload)
	}
}

func TestHistoryListAliasIsUnsupported(t *testing.T) {
	h := clitest.New(t)

	result := h.Run(t, "history", "list").RequireError(t)
	clitest.RequireYAMLError(t, result, "error.schema.yaml")
	envelope := clitest.DecodeYAMLAs[clitest.ErrorEnvelope](t, result.Stderr)
	if envelope.Error.Code != "internal_error" || !strings.Contains(envelope.Error.Message, "usage: sched history") {
		t.Fatalf("history list error = %#v", envelope.Error)
	}
}

func writeRunRecords(t *testing.T, path string, records ...sched.RunRecord) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(path), err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, record := range records {
		if err := enc.Encode(record); err != nil {
			t.Fatalf("Encode(run) error = %v", err)
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func decodeHistory(t *testing.T, raw string) clitest.HistoryResponse {
	t.Helper()
	return clitest.DecodeYAMLAs[clitest.HistoryResponse](t, raw)
}
