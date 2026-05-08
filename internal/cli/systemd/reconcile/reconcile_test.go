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

func TestReconcileActivePausedStaleAndYAMLOutput(t *testing.T) {
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
	clitest.RequireYAMLStdout(t, result, "systemd/reconcile.schema.yaml")
	reconciled := clitest.DecodeYAMLAs[sched.ReconcileResult](t, result.Stdout)
	if reconciled.DryRun || len(reconciled.Units) != 1 || reconciled.Units[0].ScheduleID != "sched-active" {
		t.Fatalf("reconcile units = %#v", reconciled)
	}
	if !contains(reconciled.Removed, "sched-command-stale.timer") {
		t.Fatalf("removed units = %#v", reconciled.Removed)
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
	clitest.RequireYAMLStdout(t, result, "systemd/reconcile.schema.yaml")
	reconciled := clitest.DecodeYAMLAs[sched.ReconcileResult](t, result.Stdout)
	if len(reconciled.Units) != 1 || reconciled.Units[0].ScheduleID != "sched-active" {
		t.Fatalf("reconcile units = %#v", reconciled.Units)
	}
	if !contains(reconciled.Removed, filepath.Base(staleService)) || !contains(reconciled.Removed, filepath.Base(staleTimer)) {
		t.Fatalf("removed units = %#v", reconciled.Removed)
	}
	if !containsSubstring(reconciled.Warnings, "suppressed sched-suppressed") {
		t.Fatalf("warnings = %#v", reconciled.Warnings)
	}

	stored := h.Run(t, "get", "sched-suppressed").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, stored, "get.schema.yaml")
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
	clitest.RequireYAMLStdout(t, result, "systemd/reconcile.schema.yaml")
	reconciled := clitest.DecodeYAMLAs[sched.ReconcileResult](t, result.Stdout)
	if !reconciled.DryRun || len(reconciled.Units) != 1 || len(reconciled.Removed) != 1 {
		t.Fatalf("dry-run YAML output = %#v", reconciled)
	}
	if _, err := os.Stat(stalePath); err != nil {
		t.Fatalf("dry-run removed stale unit: %v", err)
	}
}

func TestReconcileUsesConfiguredStableSchedLauncherPath(t *testing.T) {
	h := clitest.New(t)
	h.PutJob(t, clitest.SampleJob(clitest.WithScheduleID("sched-stable-launcher")))

	result := h.Run(t, "--sched-bin", "/data/opencode/bin/sched", "systemd", "reconcile").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, result, "systemd/reconcile.schema.yaml")
	reconciled := clitest.DecodeYAMLAs[sched.ReconcileResult](t, result.Stdout)
	if len(reconciled.Units) != 1 {
		t.Fatalf("reconcile units = %#v", reconciled.Units)
	}

	servicePath := filepath.Join(h.SystemdDir, reconciled.Units[0].ServiceName)
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

func TestReconcileUsageErrors(t *testing.T) {
	h := clitest.New(t)

	result := h.Run(t, "systemd").RequireError(t)
	clitest.RequireYAMLError(t, result, "error.schema.yaml")
	envelope := clitest.DecodeYAMLAs[clitest.ErrorEnvelope](t, result.Stderr)
	if envelope.Error.Code != "internal_error" || !strings.Contains(envelope.Error.Message, "usage: sched systemd reconcile") {
		t.Fatalf("usage envelope = %#v", envelope.Error)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsSubstring(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
