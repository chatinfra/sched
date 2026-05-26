// Spec: openspec/changes/install-sched-from-public-mirror/specs/sched-cli-command-scheduling/spec.md
package reconcile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chatinfra/sched/internal/cli/clitest"
	"github.com/chatinfra/sched/internal/sched"
)

func TestReconcileActivePausedStaleAndTerminalOutput(t *testing.T) {
	h := clitest.New(t)
	h.PutJob(t, clitest.SampleJob(clitest.WithScheduleID("sched-active")))
	h.PutJob(t, clitest.SampleJob(
		clitest.WithScheduleID("sched-paused"),
		clitest.WithOpenCodeID("opencode-paused"),
		clitest.WithCommandID("cmd-paused"),
		clitest.WithStatus(sched.StatusPaused),
	))
	stalePath := filepath.Join(h.SystemdDir, "sched-command-stale.timer")
	if err := os.WriteFile(stalePath, []byte("[Timer]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(stale) error = %v", err)
	}

	result := h.Run(t, "systemd", "reconcile").RequireSuccess(t)
	stdout := clitest.RequireTextStdout(t, result)
	if !strings.Contains(stdout, "Dry run: false") || !strings.Contains(stdout, "Reconciled units") || !strings.Contains(stdout, "sched-active") || strings.Contains(stdout, "sched-paused") {
		t.Fatalf("reconcile terminal units output = %s", stdout)
	}
	if !strings.Contains(stdout, "Removed units") || !strings.Contains(stdout, "sched-command-stale.timer") {
		t.Fatalf("removed terminal units output = %s", stdout)
	}
	if !strings.Contains(stdout, "Warnings\nNo warnings.") {
		t.Fatalf("reconcile terminal warnings empty state missing:\n%s", stdout)
	}
	if strings.Contains(stdout, "units:") || strings.Contains(stdout, "dryRun:") || strings.Contains(stdout, "{id:") || strings.Contains(stdout, "- {") {
		t.Fatalf("reconcile output contains YAML wrapper or flow-style mapping:\n%s", stdout)
	}
	full := h.Run(t, "systemd", "reconcile", "--full").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, full, "systemd/reconcile-full.schema.yaml")
	fullReconciled := clitest.DecodeYAMLAs[sched.ReconcileResult](t, full.Stdout)
	if len(fullReconciled.Units) != 1 || fullReconciled.Units[0].ScheduleID != "sched-active" || fullReconciled.Units[0].ScheduleKind == "" {
		t.Fatalf("full reconcile units = %#v", fullReconciled.Units)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stale unit still exists or stat failed: %v", err)
	}
}

func TestReconcileSuppressesActiveJobWithoutChangingStoredStatus(t *testing.T) {
	h := clitest.New(t)
	h.PutJob(t, clitest.SampleJob(clitest.WithScheduleID("sched-active")))
	suppressed := h.PutJob(t, clitest.SampleJob(
		clitest.WithScheduleID("sched-suppressed"),
		clitest.WithOpenCodeID("opencode-suppressed"),
		clitest.WithCommandID("cmd-suppressed"),
		clitest.WithTimerSuppressed("agent_disabled"),
	))
	staleService := filepath.Join(h.SystemdDir, sched.UnitBaseName(suppressed)+".service")
	staleTimer := filepath.Join(h.SystemdDir, sched.UnitBaseName(suppressed)+".timer")
	if err := os.WriteFile(staleService, []byte("[Service]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(staleService) error = %v", err)
	}
	if err := os.WriteFile(staleTimer, []byte("[Timer]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(staleTimer) error = %v", err)
	}

	result := h.Run(t, "systemd", "reconcile").RequireSuccess(t)
	stdout := clitest.RequireTextStdout(t, result)
	unitsSection := sectionBetween(stdout, "Reconciled units", "\n\nRemoved units")
	if !strings.Contains(unitsSection, "sched-active") || strings.Contains(unitsSection, "sched-suppressed") {
		t.Fatalf("reconcile terminal units = %s", stdout)
	}
	if !strings.Contains(stdout, filepath.Base(staleService)) || !strings.Contains(stdout, filepath.Base(staleTimer)) {
		t.Fatalf("removed terminal units = %s", stdout)
	}
	if !strings.Contains(stdout, "suppressed sched-suppressed") {
		t.Fatalf("terminal warnings = %s", stdout)
	}

	stored := h.Run(t, "get", "sched-suppressed", "--full").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, stored, "get-full.schema.yaml")
	job := clitest.DecodeYAMLAs[sched.Job](t, stored.Stdout)
	if job.Status != sched.StatusActive || !job.TimerSuppressed || job.SuppressionReason != "agent_disabled" {
		t.Fatalf("stored suppressed job = %#v", job)
	}
}

func TestReconcileDryRunSummaryPreservesStaleUnits(t *testing.T) {
	h := clitest.New(t)
	h.PutJob(t, clitest.SampleJob())
	stalePath := filepath.Join(h.SystemdDir, "sched-command-stale.service")
	if err := os.WriteFile(stalePath, []byte("[Service]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(stale) error = %v", err)
	}

	result := h.Run(t, "--dry-run", "systemd", "reconcile").RequireSuccess(t)
	stdout := clitest.RequireTextStdout(t, result)
	if !strings.Contains(stdout, "Dry run: true") || !strings.Contains(stdout, "sched-1") || !strings.Contains(stdout, "sched-command-stale.service") {
		t.Fatalf("dry-run terminal output = %s", stdout)
	}
	if strings.Contains(stdout, "units:") || strings.Contains(stdout, "dryRun:") || strings.Contains(stdout, "{id:") || strings.Contains(stdout, "- {") {
		t.Fatalf("reconcile output contains YAML wrapper or flow-style mapping:\n%s", stdout)
	}
	if _, err := os.Stat(stalePath); err != nil {
		t.Fatalf("dry-run removed stale unit: %v", err)
	}
}

func TestReconcileUsesConfiguredStableSchedLauncherPath(t *testing.T) {
	h := clitest.New(t)
	h.PutJob(t, clitest.SampleJob(clitest.WithScheduleID("sched-stable-launcher")))

	result := h.Run(t, "--sched-bin", "/data/opencode/bin/sched", "systemd", "reconcile").RequireSuccess(t)
	stdout := clitest.RequireTextStdout(t, result)
	if !strings.Contains(stdout, "sched-stable-launcher") {
		t.Fatalf("reconcile terminal units = %s", stdout)
	}
	full := h.Run(t, "--sched-bin", "/data/opencode/bin/sched", "systemd", "reconcile", "--full").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, full, "systemd/reconcile-full.schema.yaml")
	fullReconciled := clitest.DecodeYAMLAs[sched.ReconcileResult](t, full.Stdout)
	if len(fullReconciled.Units) != 1 {
		t.Fatalf("full reconcile units = %#v", fullReconciled.Units)
	}

	servicePath := filepath.Join(h.SystemdDir, fullReconciled.Units[0].ServiceName)
	serviceContent, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", servicePath, err)
	}
	rendered := string(serviceContent)
	if !strings.Contains(rendered, "/data/opencode/bin/sched") {
		t.Fatalf("service does not use stable launcher path:\n%s", rendered)
	}
	if strings.Contains(rendered, "/data/opencode/.cache/sched/bin/sched") {
		t.Fatalf("service pinned transient cache binary:\n%s", rendered)
	}
	if strings.Contains(rendered, "--json") {
		t.Fatalf("service should not request JSON output:\n%s", rendered)
	}
}

func TestReconcileEmptySectionsUseTerminalText(t *testing.T) {
	h := clitest.New(t)

	result := h.Run(t, "systemd", "reconcile").RequireSuccess(t)
	stdout := clitest.RequireTextStdout(t, result)
	for _, want := range []string{"Dry run: false", "No units reconciled.", "No stale units removed.", "No warnings."} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("empty reconcile output missing %q:\n%s", want, stdout)
		}
	}
}

func TestReconcileUsageErrors(t *testing.T) {
	h := clitest.New(t)

	result := h.Run(t, "systemd").RequireError(t)
	clitest.RequireYAMLError(t, result, "error.schema.yaml")
	envelope := clitest.DecodeYAMLAs[clitest.ErrorEnvelope](t, result.Stderr)
	if envelope.Error.Code != "internal_error" || !strings.Contains(envelope.Error.Message, "usage: sched systemd reconcile") {
		t.Fatalf("usage envelope = %#v", envelope.Error)
	}
}

func sectionBetween(value, start, end string) string {
	startIndex := strings.Index(value, start)
	if startIndex < 0 {
		return ""
	}
	value = value[startIndex:]
	endIndex := strings.Index(value, end)
	if endIndex < 0 {
		return value
	}
	return value[:endIndex]
}
