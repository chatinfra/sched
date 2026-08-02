package sched

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreValidationAndExport(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	job, err := store.PutJob(sampleJob(), now)
	if err != nil {
		t.Fatalf("PutJob() error = %v", err)
	}
	if job.SchemaVersion != SchemaVersion {
		t.Fatalf("schemaVersion = %q", job.SchemaVersion)
	}
	if job.Title != "ci-command:cmd-1:sched-1" {
		t.Fatalf("title = %q", job.Title)
	}

	got, err := store.GetJob(job.ScheduleID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.CommandName != "Daily-Backup" || got.AgentName != "build-agent" {
		t.Fatalf("metadata not preserved: %#v", got)
	}

	jobs, err := store.ListJobs()
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].ScheduleID != job.ScheduleID {
		t.Fatalf("ListJobs() = %#v", jobs)
	}
	exported, err := store.Export()
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if exported.SchemaVersion != SchemaVersion || len(exported.Jobs) != 1 {
		t.Fatalf("Export() = %#v", exported)
	}

	invalid := sampleJob()
	invalid.ScheduleID = "bad/missing"
	if _, err := store.PutJob(invalid, now); ErrorCode(err) != "invalid_schedule_id" {
		t.Fatalf("PutJob(invalid) code = %q err=%v", ErrorCode(err), err)
	}

}

func TestJobNotifyTargetJSONOmitEmpty(t *testing.T) {
	data, err := json.Marshal(sampleJob())
	if err != nil {
		t.Fatalf("Marshal(job without notify target) error = %v", err)
	}
	jsonText := string(data)
	for _, omitted := range []string{"notifyChannel", "notifyTo"} {
		if strings.Contains(jsonText, omitted) {
			t.Fatalf("JSON %s unexpectedly contains %q", jsonText, omitted)
		}
	}

	var legacy Job
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatalf("Unmarshal(legacy job) error = %v", err)
	}
	if legacy.NotifyChannel != nil || legacy.NotifyTo != nil {
		t.Fatalf("legacy notify target = %#v %#v", legacy.NotifyChannel, legacy.NotifyTo)
	}

	channel := "xmpp"
	to := "user@example.com"
	job := sampleJob()
	job.NotifyChannel = &channel
	job.NotifyTo = &to
	data, err = json.Marshal(job)
	if err != nil {
		t.Fatalf("Marshal(job with notify target) error = %v", err)
	}
	jsonText = string(data)
	for _, want := range []string{`"notifyChannel":"xmpp"`, `"notifyTo":"user@example.com"`} {
		if !strings.Contains(jsonText, want) {
			t.Fatalf("JSON %s missing %q", jsonText, want)
		}
	}
}

func TestCronAndSystemdRendering(t *testing.T) {
	job := sampleJob()
	job.ScheduleExpression = "0 9 1 * 1"
	job.Timezone = "America/New_York"
	unitDir := tempDir(t)
	home := tempDir(t)
	plan, calendars, err := RenderSystemdUnit(job, SystemdOptions{UnitDir: unitDir, OpenCodeHome: home, StateRoot: filepath.Join(home, "state"), SchedBin: "/usr/local/bin/sched"})
	if err != nil {
		t.Fatalf("RenderSystemdUnit() error = %v", err)
	}
	if len(calendars) != 2 {
		t.Fatalf("expected split day-of-month/day-of-week calendars, got %#v", calendars)
	}
	for _, calendar := range calendars {
		if !strings.Contains(calendar, "America/New_York") {
			t.Fatalf("calendar missing timezone: %q", calendar)
		}
	}
	if !strings.Contains(plan.TimerContent, "Persistent=false") {
		t.Fatalf("timer missing Persistent=false:\n%s", plan.TimerContent)
	}
	if strings.Contains(plan.TimerContent, "Timezone=") {
		t.Fatalf("timer unexpectedly contains Timezone=:\n%s", plan.TimerContent)
	}
	if !strings.HasPrefix(plan.TimerName, "sched-command-") || !strings.HasSuffix(plan.TimerName, ".timer") {
		t.Fatalf("timer name = %q", plan.TimerName)
	}
	for _, omitted := range []string{"SCHED_NOTIFY_CHANNEL", "SCHED_NOTIFY_TO"} {
		if strings.Contains(plan.ServiceContent, omitted) {
			t.Fatalf("service unexpectedly contains %q:\n%s", omitted, plan.ServiceContent)
		}
	}
}

func TestSystemdRenderingIncludesNotifyEnvironment(t *testing.T) {
	channel := "xmpp"
	to := "user@example.com"
	job := sampleJob()
	job.NotifyChannel = &channel
	job.NotifyTo = &to
	plan, _, err := RenderSystemdUnit(job, SystemdOptions{UnitDir: tempDir(t), OpenCodeHome: tempDir(t), StateRoot: filepath.Join(tempDir(t), "state"), SchedBin: "/usr/local/bin/sched"})
	if err != nil {
		t.Fatalf("RenderSystemdUnit() error = %v", err)
	}
	for _, want := range []string{
		"Environment=SCHED_NOTIFY_CHANNEL=xmpp",
		"Environment=SCHED_NOTIFY_TO=user@example.com",
	} {
		if !strings.Contains(plan.ServiceContent, want) {
			t.Fatalf("service missing %q:\n%s", want, plan.ServiceContent)
		}
	}
}

func TestIntervalSystemdRenderingUsesSchedEnvelope(t *testing.T) {
	job := sampleJob()
	job.ScheduleKind = ScheduleKindInterval
	job.ScheduleExpression = ""
	job.Timezone = ""
	job.IntervalDuration = "5s"
	unitDir := tempDir(t)
	home := tempDir(t)

	plan, calendars, err := RenderSystemdUnit(job, SystemdOptions{UnitDir: unitDir, OpenCodeHome: home, StateRoot: filepath.Join(home, "state"), SchedBin: "/usr/local/bin/sched"})
	if err != nil {
		t.Fatalf("RenderSystemdUnit(interval) error = %v", err)
	}
	if len(calendars) != 0 {
		t.Fatalf("interval calendars = %#v", calendars)
	}
	for _, want := range []string{"OnBootSec=5s", "OnUnitActiveSec=5s", "Persistent=false"} {
		if !strings.Contains(plan.TimerContent, want) {
			t.Fatalf("interval timer missing %q:\n%s", want, plan.TimerContent)
		}
	}
	if strings.Contains(plan.TimerContent, "OnCalendar=") {
		t.Fatalf("interval timer contains cron calendar:\n%s", plan.TimerContent)
	}
	for _, want := range []string{"run", job.ScheduleID, "--source scheduled"} {
		if !strings.Contains(plan.ServiceContent, want) {
			t.Fatalf("interval service missing %q:\n%s", want, plan.ServiceContent)
		}
	}
	if strings.Contains(plan.ServiceContent, "--json") {
		t.Fatalf("interval service should not request JSON output:\n%s", plan.ServiceContent)
	}
	if strings.Contains(plan.ServiceContent, "opencode run") {
		t.Fatalf("interval service invokes opencode directly:\n%s", plan.ServiceContent)
	}
}

func TestJobValidationSupportsCronAndIntervalKinds(t *testing.T) {
	store := newTestStore(t)
	cron, err := store.PutJob(sampleJob(), time.Now())
	if err != nil {
		t.Fatalf("PutJob(cron) error = %v", err)
	}
	if cron.ScheduleKind != ScheduleKindCron || cron.ScheduleExpression == "" || cron.Timezone == "" {
		t.Fatalf("cron job normalization = %#v", cron)
	}

	interval := sampleJob()
	interval.ScheduleID = "interval-1"
	interval.ScheduleKind = ScheduleKindInterval
	interval.ScheduleExpression = ""
	interval.Timezone = ""
	interval.IntervalDuration = "5s"
	persisted, err := store.PutJob(interval, time.Now())
	if err != nil {
		t.Fatalf("PutJob(interval) error = %v", err)
	}
	if persisted.ScheduleKind != ScheduleKindInterval || persisted.IntervalDuration != "5s" || persisted.ScheduleExpression != "" || persisted.Timezone != "" {
		t.Fatalf("interval job normalization = %#v", persisted)
	}

	for _, value := range []string{"", "0s", "-5s", "not-a-duration"} {
		rejected := interval
		rejected.ScheduleID = "interval-bad-" + token(value, "empty")
		rejected.IntervalDuration = value
		if _, err := store.PutJob(rejected, time.Now()); ErrorCode(err) != "invalid_interval" {
			t.Fatalf("PutJob(interval duration %q) code = %q err=%v", value, ErrorCode(err), err)
		}
	}
}

func TestReconcileActivePausedAndStaleUnits(t *testing.T) {
	store := newTestStore(t)
	unitDir := tempDir(t)
	home := tempDir(t)
	if _, err := store.PutJob(sampleJob(), time.Now()); err != nil {
		t.Fatalf("PutJob() error = %v", err)
	}
	stalePath := filepath.Join(unitDir, "sched-command-stale.timer")
	if err := os.WriteFile(stalePath, []byte("[Timer]\n"), 0o644); err != nil {
		t.Fatalf("write stale unit: %v", err)
	}

	result, err := ReconcileSystemd(store, SystemdOptions{UnitDir: unitDir, OpenCodeHome: home, StateRoot: store.Root, SchedBin: "sched", Apply: false})
	if err != nil {
		t.Fatalf("ReconcileSystemd() error = %v", err)
	}
	if len(result.Units) != 1 {
		t.Fatalf("Units = %#v", result.Units)
	}
	if len(result.Removed) != 1 || result.Removed[0] != "sched-command-stale.timer" {
		t.Fatalf("Removed = %#v", result.Removed)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stale unit still exists or stat failed: %v", err)
	}

	if _, err := ReconcileSystemd(store, SystemdOptions{UnitDir: unitDir, OpenCodeHome: home, StateRoot: store.Root, SchedBin: "sched", Apply: false}); err != nil {
		t.Fatalf("second ReconcileSystemd() error = %v", err)
	}
	entries, err := os.ReadDir(unitDir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected service+timer only, got %d entries", len(entries))
	}

	paused := sampleJob()
	paused.Status = StatusPaused
	if _, err := store.PutJob(paused, time.Now()); err != nil {
		t.Fatalf("PutJob(paused) error = %v", err)
	}
	pausedResult, err := ReconcileSystemd(store, SystemdOptions{UnitDir: unitDir, OpenCodeHome: home, StateRoot: store.Root, SchedBin: "sched", Apply: false})
	if err != nil {
		t.Fatalf("ReconcileSystemd(paused) error = %v", err)
	}
	if len(pausedResult.Units) != 0 || len(pausedResult.Removed) != 2 {
		t.Fatalf("paused reconcile = %#v", pausedResult)
	}
}

func TestReconcileFromRepoInstallsOperatorUnits(t *testing.T) {
	store := newTestStore(t)
	repoRoot, repoUnitDir := newTestRepoUnitDir(t)
	writeOperatorUnitPair(t, repoUnitDir, "operator-backup", "operator-backup",
		"ExecStart=/bin/true --apply",
		"OnCalendar=*-*-* 02:00:00 UTC\nPersistent=true",
	)
	unitDir := tempDir(t)
	systemctlBin, systemctlLog := fakeSystemctl(t)

	result, err := ReconcileSystemd(store, SystemdOptions{UnitDir: unitDir, StateRoot: store.Root, OpenCodeHome: tempDir(t), FromRepoDir: repoUnitDir, RepoRoot: repoRoot, SystemctlBin: systemctlBin, Apply: true})
	if err != nil {
		t.Fatalf("ReconcileSystemd() error = %v", err)
	}
	if len(result.Units) != 1 || result.Units[0].ScheduleID != "operator-backup" || result.Units[0].TimerName != "operator-backup.timer" {
		t.Fatalf("Units = %#v", result.Units)
	}
	service := readTextFile(t, filepath.Join(unitDir, "operator-backup.service"))
	timer := readTextFile(t, filepath.Join(unitDir, "operator-backup.timer"))
	for _, rendered := range []string{service, timer} {
		if !strings.Contains(rendered, "[X-Sched]") || !strings.Contains(rendered, "Managed=true") || !strings.Contains(rendered, "Id=operator-backup") {
			t.Fatalf("rendered unit missing [X-Sched] metadata:\n%s", rendered)
		}
		if strings.Contains(rendered, "@REPO_ROOT@") {
			t.Fatalf("rendered unit still contains repo placeholder:\n%s", rendered)
		}
	}
	if !strings.Contains(service, "ExecStart=/bin/true --apply") {
		t.Fatalf("service ExecStart changed:\n%s", service)
	}
	if strings.Contains(service, "sched run") {
		t.Fatalf("operator service should run directly, not through sched run:\n%s", service)
	}
	if !strings.Contains(readTextFile(t, systemctlLog), "--user enable --now operator-backup.timer") {
		t.Fatalf("systemctl log missing timer enable:\n%s", readTextFile(t, systemctlLog))
	}
	if !strings.Contains(timer, "OnCalendar=*-*-* 02:00:00 UTC") || !strings.Contains(timer, "Persistent=true") {
		t.Fatalf("timer content changed:\n%s", timer)
	}
}

func TestReconcileFromRepoPrunesRemovedOperatorUnit(t *testing.T) {
	store := newTestStore(t)
	repoRoot, repoUnitDir := newTestRepoUnitDir(t)
	writeOperatorUnitPair(t, repoUnitDir, "kept-operator", "kept-operator",
		"ExecStart=/bin/true",
		"OnCalendar=*-*-* 03:00:00 UTC\nPersistent=true",
	)
	unitDir := tempDir(t)
	writeInstalledOperatorUnitPair(t, unitDir, "removed-operator", "removed-operator")
	writeInstalledOperatorUnitPair(t, unitDir, "kept-operator", "kept-operator")
	systemctlBin, systemctlLog := fakeSystemctl(t)

	result, err := ReconcileSystemd(store, SystemdOptions{UnitDir: unitDir, StateRoot: store.Root, OpenCodeHome: tempDir(t), FromRepoDir: repoUnitDir, RepoRoot: repoRoot, SystemctlBin: systemctlBin, Apply: true})
	if err != nil {
		t.Fatalf("ReconcileSystemd() error = %v", err)
	}
	wantRemoved := []string{"removed-operator.service", "removed-operator.timer"}
	if strings.Join(result.Removed, ",") != strings.Join(wantRemoved, ",") {
		t.Fatalf("Removed = %#v, want %#v", result.Removed, wantRemoved)
	}
	for _, name := range wantRemoved {
		if _, err := os.Stat(filepath.Join(unitDir, name)); !os.IsNotExist(err) {
			t.Fatalf("removed unit %s still exists or stat failed: %v", name, err)
		}
	}
	keptService := readTextFile(t, filepath.Join(unitDir, "kept-operator.service"))
	if !strings.Contains(keptService, "ExecStart=/bin/true") {
		t.Fatalf("kept operator was not re-rendered:\n%s", keptService)
	}
	log := readTextFile(t, systemctlLog)
	for _, want := range []string{"--user disable --now removed-operator.timer", "--user stop removed-operator.service"} {
		if !strings.Contains(log, want) {
			t.Fatalf("systemctl log missing %q:\n%s", want, log)
		}
	}
}

func TestReconcileOperatorAndAgentCoexist(t *testing.T) {
	store := newTestStore(t)
	job, err := store.PutJob(sampleJob(), time.Now())
	if err != nil {
		t.Fatalf("PutJob() error = %v", err)
	}
	repoRoot, repoUnitDir := newTestRepoUnitDir(t)
	writeOperatorUnitPair(t, repoUnitDir, "operator-task", "operator-task",
		"ExecStart=/bin/true",
		"OnBootSec=2min\nOnUnitActiveSec=2min\nPersistent=false",
	)
	unitDir := tempDir(t)

	result, err := ReconcileSystemd(store, SystemdOptions{UnitDir: unitDir, StateRoot: store.Root, OpenCodeHome: tempDir(t), FromRepoDir: repoUnitDir, RepoRoot: repoRoot, SchedBin: "sched", Apply: false})
	if err != nil {
		t.Fatalf("ReconcileSystemd() error = %v", err)
	}
	if len(result.Units) != 2 || len(result.Removed) != 0 {
		t.Fatalf("coexist reconcile = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(unitDir, UnitBaseName(job)+".timer")); err != nil {
		t.Fatalf("agent timer missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(unitDir, "operator-task.timer")); err != nil {
		t.Fatalf("operator timer missing: %v", err)
	}
}

func TestAgentReconcileDoesNotPruneOperatorUnit(t *testing.T) {
	store := newTestStore(t)
	unitDir := tempDir(t)
	writeInstalledOperatorUnitPair(t, unitDir, "operator-task", "operator-task")

	result, err := ReconcileSystemd(store, SystemdOptions{UnitDir: unitDir, StateRoot: store.Root, OpenCodeHome: tempDir(t), Apply: false})
	if err != nil {
		t.Fatalf("ReconcileSystemd() error = %v", err)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("Removed = %#v, want none", result.Removed)
	}
	for _, name := range []string{"operator-task.service", "operator-task.timer"} {
		if _, err := os.Stat(filepath.Join(unitDir, name)); err != nil {
			t.Fatalf("operator unit %s was pruned: %v", name, err)
		}
	}
}

func TestOperatorReconcileDoesNotPruneAgentUnit(t *testing.T) {
	store := newTestStore(t)
	repoRoot, repoUnitDir := newTestRepoUnitDir(t)
	unitDir := tempDir(t)
	for _, name := range []string{"sched-command-stale.service", "sched-command-stale.timer"} {
		if err := os.WriteFile(filepath.Join(unitDir, name), []byte("[Unit]\nDescription=stale agent unit\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}

	result, err := ReconcileSystemd(store, SystemdOptions{UnitDir: unitDir, StateRoot: store.Root, OpenCodeHome: tempDir(t), FromRepoDir: repoUnitDir, RepoRoot: repoRoot, Apply: false})
	if err != nil {
		t.Fatalf("ReconcileSystemd() error = %v", err)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("Removed = %#v, want none", result.Removed)
	}
	for _, name := range []string{"sched-command-stale.service", "sched-command-stale.timer"} {
		if _, err := os.Stat(filepath.Join(unitDir, name)); err != nil {
			t.Fatalf("agent unit %s was pruned: %v", name, err)
		}
	}
}

func TestOperatorReconcileDoesNotPruneSuperdirServeUnit(t *testing.T) {
	store := newTestStore(t)
	repoRoot, repoUnitDir := newTestRepoUnitDir(t)
	writeOperatorUnitPair(t, repoUnitDir, "operator-backup", "operator-backup",
		"ExecStart=/bin/true",
		"OnCalendar=*-*-* 03:00:00 UTC\nPersistent=true",
	)
	unitDir := tempDir(t)
	servePath := filepath.Join(unitDir, "superdir-serve.service")
	serveContent := "[Unit]\nDescription=Superdir serve\n\n[Service]\nExecStart=/usr/local/bin/superdir serve\n\n[X-Superdir]\nManaged=true\nId=superdir-serve\n"
	if err := os.WriteFile(servePath, []byte(serveContent), 0o644); err != nil {
		t.Fatalf("WriteFile(superdir-serve.service) error = %v", err)
	}
	systemctlBin, systemctlLog := fakeSystemctl(t)

	result, err := ReconcileSystemd(store, SystemdOptions{UnitDir: unitDir, StateRoot: store.Root, OpenCodeHome: tempDir(t), FromRepoDir: repoUnitDir, RepoRoot: repoRoot, SystemctlBin: systemctlBin, Apply: true})
	if err != nil {
		t.Fatalf("ReconcileSystemd() error = %v", err)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("Removed = %#v, want none", result.Removed)
	}
	if got := readTextFile(t, servePath); got != serveContent {
		t.Fatalf("superdir-serve.service changed across reconcile:\n%s", got)
	}
	if log := readTextFile(t, systemctlLog); strings.Contains(log, "superdir-serve") {
		t.Fatalf("operator reconcile touched superdir-serve:\n%s", log)
	}
}

func TestOperatorReconcileReservesSuperdirServeFromPruneAfterMarkerDrift(t *testing.T) {
	store := newTestStore(t)
	repoRoot, repoUnitDir := newTestRepoUnitDir(t)
	unitDir := tempDir(t)
	writeInstalledOperatorUnitPair(t, unitDir, "superdir-serve", "superdir-serve")

	result, err := ReconcileSystemd(store, SystemdOptions{UnitDir: unitDir, StateRoot: store.Root, OpenCodeHome: tempDir(t), FromRepoDir: repoUnitDir, RepoRoot: repoRoot, Apply: false})
	if err != nil {
		t.Fatalf("ReconcileSystemd() error = %v", err)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("Removed = %#v, want none", result.Removed)
	}
	for _, name := range []string{"superdir-serve.service", "superdir-serve.timer"} {
		if _, err := os.Stat(filepath.Join(unitDir, name)); err != nil {
			t.Fatalf("reserved unit %s was pruned: %v", name, err)
		}
	}
}

func TestReconcileRejectsReservedOperatorName(t *testing.T) {
	for _, reservedBase := range []string{"sched-command-bad", "superdir-serve"} {
		t.Run(reservedBase, func(t *testing.T) {
			store := newTestStore(t)
			job, err := store.PutJob(sampleJob(), time.Now())
			if err != nil {
				t.Fatalf("PutJob() error = %v", err)
			}
			repoRoot, repoUnitDir := newTestRepoUnitDir(t)
			writeOperatorUnitPair(t, repoUnitDir, reservedBase, reservedBase,
				"ExecStart=@REPO_ROOT@/bin/bad",
				"OnCalendar=*-*-* 04:00:00 UTC\nPersistent=true",
			)
			unitDir := tempDir(t)
			writeInstalledOperatorUnitPair(t, unitDir, "old-operator", "old-operator")

			_, err = ReconcileSystemd(store, SystemdOptions{UnitDir: unitDir, StateRoot: store.Root, OpenCodeHome: tempDir(t), FromRepoDir: repoUnitDir, RepoRoot: repoRoot, SchedBin: "sched", Apply: false})
			if ErrorCode(err) != "reserved_operator_unit_name" {
				t.Fatalf("ReconcileSystemd() code = %q err=%v", ErrorCode(err), err)
			}
			for _, name := range []string{reservedBase + ".service", reservedBase + ".timer", UnitBaseName(job) + ".service", UnitBaseName(job) + ".timer"} {
				if _, err := os.Stat(filepath.Join(unitDir, name)); !os.IsNotExist(err) {
					t.Fatalf("unit %s was written despite reserved-name rejection: %v", name, err)
				}
			}
			for _, name := range []string{"old-operator.service", "old-operator.timer"} {
				if _, err := os.Stat(filepath.Join(unitDir, name)); err != nil {
					t.Fatalf("existing operator unit %s was pruned despite rejection: %v", name, err)
				}
			}
		})
	}
}

func TestReconcileSubstitutesDeclaredRepoRoot(t *testing.T) {
	store := newTestStore(t)
	fromRepoRoot, repoUnitDir := newTestRepoUnitDir(t)
	declaredRoot := tempDir(t)
	writeOperatorUnitPair(t, repoUnitDir, "operator-declared-root", "operator-declared-root",
		"ExecStart=/bin/true",
		"OnCalendar=*-*-* 02:00:00 UTC\nPersistent=true",
	)
	unitDir := tempDir(t)

	_, err := ReconcileSystemd(store, SystemdOptions{UnitDir: unitDir, StateRoot: store.Root, OpenCodeHome: tempDir(t), FromRepoDir: repoUnitDir, RepoRoot: declaredRoot})
	if err != nil {
		t.Fatalf("ReconcileSystemd() error = %v", err)
	}
	service := readTextFile(t, filepath.Join(unitDir, "operator-declared-root.service"))
	for _, want := range []string{"WorkingDirectory=" + declaredRoot, "EnvironmentFile=-" + filepath.Join(declaredRoot, ".env")} {
		if !strings.Contains(service, want) {
			t.Fatalf("service missing declared root value %q:\n%s", want, service)
		}
	}
	if strings.Contains(service, fromRepoRoot) || strings.Contains(service, "@REPO_ROOT@") {
		t.Fatalf("service used source checkout instead of declared root:\n%s", service)
	}
}

func TestReconcileFailsClosedWithoutDeclaredRepoRoot(t *testing.T) {
	store := newTestStore(t)
	_, repoUnitDir := newTestRepoUnitDir(t)
	writeOperatorUnitPair(t, repoUnitDir, "operator-missing-root", "operator-missing-root",
		"ExecStart=/bin/true",
		"OnCalendar=*-*-* 02:00:00 UTC\nPersistent=true",
	)
	unitDir := filepath.Join(tempDir(t), "units")

	_, err := ReconcileSystemd(store, SystemdOptions{UnitDir: unitDir, StateRoot: store.Root, OpenCodeHome: tempDir(t), FromRepoDir: repoUnitDir})
	if ErrorCode(err) != "missing_repo_root" {
		t.Fatalf("ReconcileSystemd() code = %q err=%v", ErrorCode(err), err)
	}
	if _, statErr := os.Stat(unitDir); !os.IsNotExist(statErr) {
		t.Fatalf("unit directory was created despite missing-root rejection: %v", statErr)
	}
}

func TestReconcileRejectsRepoRelativeExecStart(t *testing.T) {
	store := newTestStore(t)
	repoRoot, repoUnitDir := newTestRepoUnitDir(t)
	binDir := filepath.Join(repoRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(bin) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "operator-repo-exec"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(operator executable) error = %v", err)
	}
	writeOperatorUnitPair(t, repoUnitDir, "operator-repo-exec", "operator-repo-exec",
		"ExecStart=@REPO_ROOT@/bin/operator-repo-exec",
		"OnCalendar=*-*-* 02:00:00 UTC\nPersistent=true",
	)
	unitDir := filepath.Join(tempDir(t), "units")

	_, err := ReconcileSystemd(store, SystemdOptions{UnitDir: unitDir, StateRoot: store.Root, OpenCodeHome: tempDir(t), FromRepoDir: repoUnitDir, RepoRoot: repoRoot})
	if ErrorCode(err) != "repo_contained_exec_start" || !strings.Contains(ErrorMessage(err), "operator-repo-exec.service") {
		t.Fatalf("ReconcileSystemd() code/message = %q/%q", ErrorCode(err), ErrorMessage(err))
	}
	if _, statErr := os.Stat(unitDir); !os.IsNotExist(statErr) {
		t.Fatalf("unit directory was created despite repo ExecStart rejection: %v", statErr)
	}
}

func TestReconcileRejectsMissingExecStartBinary(t *testing.T) {
	for _, test := range []struct {
		name     string
		execPath func(*testing.T) string
	}{
		{name: "missing", execPath: func(t *testing.T) string { return filepath.Join(tempDir(t), "missing") }},
		{name: "non-executable", execPath: func(t *testing.T) string {
			path := filepath.Join(tempDir(t), "not-executable")
			if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o644); err != nil {
				t.Fatalf("WriteFile(non-executable) error = %v", err)
			}
			return path
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			repoRoot, repoUnitDir := newTestRepoUnitDir(t)
			writeOperatorUnitPair(t, repoUnitDir, "operator-invalid-exec", "operator-invalid-exec",
				"ExecStart="+test.execPath(t),
				"OnCalendar=*-*-* 02:00:00 UTC\nPersistent=true",
			)
			unitDir := filepath.Join(tempDir(t), "units")

			_, err := ReconcileSystemd(store, SystemdOptions{UnitDir: unitDir, StateRoot: store.Root, OpenCodeHome: tempDir(t), FromRepoDir: repoUnitDir, RepoRoot: repoRoot, DryRun: true})
			if ErrorCode(err) != "invalid_exec_start" || !strings.Contains(ErrorMessage(err), "operator-invalid-exec.service") {
				t.Fatalf("ReconcileSystemd() code/message = %q/%q", ErrorCode(err), ErrorMessage(err))
			}
			if _, statErr := os.Stat(unitDir); !os.IsNotExist(statErr) {
				t.Fatalf("dry-run created unit directory despite ExecStart rejection: %v", statErr)
			}
		})
	}
}

func TestXSchedSectionParse(t *testing.T) {
	metadata := parseXSchedSection(`[Unit]
Description=demo

[X-Sched]
Managed=true # sched-owned
Id=demo-id ; stable id
Unknown=ignored
`)
	if !metadata.Present || !metadata.Managed || metadata.ID != "demo-id" {
		t.Fatalf("metadata = %#v", metadata)
	}
	missing := parseXSchedSection("[Unit]\nDescription=plain unit\n")
	if missing.Present || missing.Managed || missing.ID != "" {
		t.Fatalf("missing metadata = %#v", missing)
	}
	unknownOnly := parseXSchedSection("[X-Sched]\nOther=value\n")
	if !unknownOnly.Present || unknownOnly.Managed || unknownOnly.ID != "" {
		t.Fatalf("unknown-only metadata = %#v", unknownOnly)
	}
}

func TestRepoOperatorUnitsRenderCleanly(t *testing.T) {
	store := newTestStore(t)
	repoRoot := workspaceRoot(t)
	sourceUnitDir := filepath.Join(repoRoot, "systemd", "user")
	_, repoUnitDir := newTestRepoUnitDir(t)
	entries, err := os.ReadDir(sourceUnitDir)
	if err != nil {
		t.Fatalf("ReadDir(sourceUnitDir) error = %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sourceUnitDir, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", entry.Name(), err)
		}
		content := string(data)
		switch entry.Name() {
		case "cicl-backup-c0.service":
			if !strings.Contains(content, "ExecStart=/usr/local/bin/cicl backup create --id c0") {
				t.Fatalf("checked-in c0 ExecStart unexpected:\n%s", content)
			}
		case "reap-idle-build-daemons.service":
			if !strings.Contains(content, "ExecStart=/usr/bin/python3 bin/reap_idle_daemons --apply") {
				t.Fatalf("checked-in reaper ExecStart unexpected:\n%s", content)
			}
		}
		content = strings.ReplaceAll(content, "/usr/local/bin/cicl", "/bin/true")
		content = strings.ReplaceAll(content, "/usr/bin/python3", "/bin/true")
		if err := os.WriteFile(filepath.Join(repoUnitDir, entry.Name()), []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", entry.Name(), err)
		}
	}
	unitDir := tempDir(t)

	result, err := ReconcileSystemd(store, SystemdOptions{UnitDir: unitDir, StateRoot: store.Root, OpenCodeHome: tempDir(t), FromRepoDir: repoUnitDir, RepoRoot: repoRoot, DryRun: true})
	if err != nil {
		t.Fatalf("ReconcileSystemd(dry-run) error = %v", err)
	}
	if len(result.Units) != 2 || !result.DryRun {
		t.Fatalf("dry-run result = %#v", result)
	}
	for _, name := range []string{"cicl-backup-c0.service", "reap-idle-build-daemons.service"} {
		if _, err := os.Stat(filepath.Join(unitDir, name)); !os.IsNotExist(err) {
			t.Fatalf("dry-run wrote %s: %v", name, err)
		}
	}
	plans, _, warnings, err := repoOperatorUnitPlans(SystemdOptions{UnitDir: unitDir, FromRepoDir: repoUnitDir, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("repoOperatorUnitPlans() error = %v", err)
	}
	if len(warnings) != 0 || len(plans) != 2 {
		t.Fatalf("repo plans = %#v warnings=%#v", plans, warnings)
	}
	byID := map[string]UnitPlan{}
	for _, plan := range plans {
		byID[plan.ScheduleID] = plan
		for _, content := range []string{plan.ServiceContent, plan.TimerContent} {
			metadata := parseXSchedSection(content)
			if !metadata.Managed || metadata.ID != plan.ScheduleID {
				t.Fatalf("rendered metadata for %s = %#v\n%s", plan.ScheduleID, metadata, content)
			}
			if strings.Contains(content, "@REPO_ROOT@") {
				t.Fatalf("rendered content for %s still contains placeholder:\n%s", plan.ScheduleID, content)
			}
		}
	}
	unconfiguredClusterID := "c1"
	orphanedScheduleID := "cicl-backup-" + unconfiguredClusterID
	for _, name := range []string{orphanedScheduleID + ".service", orphanedScheduleID + ".timer"} {
		if _, err := os.Stat(filepath.Join(sourceUnitDir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s must remain absent: a cicl-backup-<id> pair may only be checked in once cicl status --id <id> exits zero (stat error: %v)", name, err)
		}
	}
	if _, exists := byID[orphanedScheduleID]; exists {
		t.Fatalf("%s plan must remain absent: a cicl-backup-<id> pair may only be checked in once cicl status --id <id> exits zero", orphanedScheduleID)
	}
	cicl := byID["cicl-backup-c0"]
	if !strings.Contains(cicl.ServiceContent, "ExecStart=/bin/true backup create --id c0") || strings.Contains(cicl.ServiceContent, "sched run") {
		t.Fatalf("cicl service content unexpected:\n%s", cicl.ServiceContent)
	}
	if !strings.Contains(cicl.TimerContent, "OnCalendar=*-*-* 02:00:00 UTC") || !strings.Contains(cicl.TimerContent, "Persistent=true") {
		t.Fatalf("cicl timer content unexpected:\n%s", cicl.TimerContent)
	}
	reaper := byID["reap-idle-build-daemons"]
	if !strings.Contains(reaper.ServiceContent, "ExecStart=/bin/true bin/reap_idle_daemons --apply") || strings.Contains(reaper.ServiceContent, "sched run") {
		t.Fatalf("reaper service content unexpected:\n%s", reaper.ServiceContent)
	}
	for _, want := range []string{"OnBootSec=2min", "OnUnitActiveSec=2min", "Persistent=false"} {
		if !strings.Contains(reaper.TimerContent, want) {
			t.Fatalf("reaper timer missing %q:\n%s", want, reaper.TimerContent)
		}
	}
}

func TestRunJobHistoryEnvironmentAndLocking(t *testing.T) {
	store := newTestStore(t)
	channel := "xmpp"
	to := "user@example.com"
	seed := sampleJob()
	seed.NotifyChannel = &channel
	seed.NotifyTo = &to
	job, err := store.PutJob(seed, time.Now())
	if err != nil {
		t.Fatalf("PutJob() error = %v", err)
	}
	home := tempDir(t)
	opencode := filepath.Join(tempDir(t), "opencode")
	stub := "#!/bin/sh\n" +
		"printf 'OPENCODE_LOG_DIR=%s\\n' \"$OPENCODE_LOG_DIR\"\n" +
		"printf 'OPENCODE_PERMISSION=%s\\n' \"$OPENCODE_PERMISSION\"\n" +
		"printf 'SCHED_NOTIFY_CHANNEL=%s\\n' \"$SCHED_NOTIFY_CHANNEL\"\n" +
		"printf 'SCHED_NOTIFY_TO=%s\\n' \"$SCHED_NOTIFY_TO\"\n" +
		"printf 'args=%s\\n' \"$*\"\n"
	if err := os.WriteFile(opencode, []byte(stub), 0o755); err != nil {
		t.Fatalf("write opencode stub: %v", err)
	}
	record, err := RunJob(store, job.ScheduleID, RunOptions{Source: RunSourceManual, OpenCodeBin: opencode, OpenCodeHome: home})
	if err != nil {
		t.Fatalf("RunJob() error = %v", err)
	}
	if record.Status != RunStatusSuccess || record.ExitCode == nil || *record.ExitCode != 0 {
		t.Fatalf("record = %#v", record)
	}
	logData, err := os.ReadFile(record.LogPath)
	if err != nil {
		t.Fatalf("read run log: %v", err)
	}
	logText := string(logData)
	for _, want := range []string{"OPENCODE_LOG_DIR=" + filepath.Join(home, "log"), `OPENCODE_PERMISSION={"question":"deny"}`, "SCHED_NOTIFY_CHANNEL=xmpp", "SCHED_NOTIFY_TO=user@example.com", "--title ci-command:cmd-1:sched-1"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("run log missing %q:\n%s", want, logText)
		}
	}
	runs, err := store.ListRuns(job.ScheduleID, 10)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].RunID != record.RunID {
		t.Fatalf("runs = %#v", runs)
	}

	lock := lockRecord{ScheduleID: job.ScheduleID, PID: os.Getpid(), StartedAt: time.Now().UTC()}
	data, _ := json.Marshal(lock)
	if err := os.WriteFile(store.lockPath(job.ScheduleID), data, 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	skipped, err := RunJob(store, job.ScheduleID, RunOptions{Source: RunSourceScheduled, OpenCodeBin: opencode, OpenCodeHome: home})
	if err != nil {
		t.Fatalf("RunJob(locked) error = %v", err)
	}
	if skipped.Status != RunStatusSkipped {
		t.Fatalf("locked run status = %#v", skipped)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(filepath.Join(tempDir(t), "state"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

func sampleJob() Job {
	return Job{
		ScheduleID:         "sched-1",
		TenantID:           "tenant-1",
		OpenCodeID:         "opencode-1",
		AgentID:            "agent-1",
		AgentName:          "build-agent",
		CommandID:          "cmd-1",
		CommandName:        "Daily-Backup",
		ScheduleExpression: "0 9 * * *",
		Timezone:           "UTC",
		Status:             StatusActive,
		Workdir:            "/data/opencode/work",
	}
}

func tempDir(t *testing.T) string {
	t.Helper()
	base := os.Getenv("SUPER_TMP_DIR")
	if base == "" {
		base = os.Getenv("TMPDIR")
	}
	if base == "" {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, "sched-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(data)
}

func fakeSystemctl(t *testing.T) (string, string) {
	t.Helper()
	dir := tempDir(t)
	path := filepath.Join(dir, "systemctl")
	logPath := filepath.Join(dir, "systemctl.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$SYSTEMCTL_LOG\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(systemctl) error = %v", err)
	}
	t.Setenv("SYSTEMCTL_LOG", logPath)
	return path, logPath
}

func newTestRepoUnitDir(t *testing.T) (string, string) {
	t.Helper()
	repoRoot := tempDir(t)
	if err := os.Mkdir(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir(.git) error = %v", err)
	}
	repoUnitDir := filepath.Join(repoRoot, "systemd", "user")
	if err := os.MkdirAll(repoUnitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(repoUnitDir) error = %v", err)
	}
	return repoRoot, repoUnitDir
}

func writeOperatorUnitPair(t *testing.T, repoUnitDir, base, id, serviceBody, timerBody string) {
	t.Helper()
	service := "[Unit]\nDescription=" + base + " service\n\n[Service]\nType=oneshot\nWorkingDirectory=@REPO_ROOT@\nEnvironmentFile=-@REPO_ROOT@/.env\n" + serviceBody + "\n\n[X-Sched]\nManaged=true\nId=" + id + "\n"
	timer := "[Unit]\nDescription=" + base + " timer\n\n[Timer]\nUnit=" + base + ".service\n" + timerBody + "\n\n[Install]\nWantedBy=timers.target\n\n[X-Sched]\nManaged=true\nId=" + id + "\n"
	if err := os.WriteFile(filepath.Join(repoUnitDir, base+".service"), []byte(service), 0o644); err != nil {
		t.Fatalf("WriteFile(%s.service) error = %v", base, err)
	}
	if err := os.WriteFile(filepath.Join(repoUnitDir, base+".timer"), []byte(timer), 0o644); err != nil {
		t.Fatalf("WriteFile(%s.timer) error = %v", base, err)
	}
}

func writeInstalledOperatorUnitPair(t *testing.T, unitDir, base, id string) {
	t.Helper()
	service := "[Unit]\nDescription=" + base + " service\n\n[Service]\nType=oneshot\nExecStart=/bin/true\n\n[X-Sched]\nManaged=true\nId=" + id + "\n"
	timer := "[Unit]\nDescription=" + base + " timer\n\n[Timer]\nUnit=" + base + ".service\nOnCalendar=*-*-* 01:00:00 UTC\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n\n[X-Sched]\nManaged=true\nId=" + id + "\n"
	if err := os.WriteFile(filepath.Join(unitDir, base+".service"), []byte(service), 0o644); err != nil {
		t.Fatalf("WriteFile(installed service) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(unitDir, base+".timer"), []byte(timer), 0o644); err != nil {
		t.Fatalf("WriteFile(installed timer) error = %v", err)
	}
}

func workspaceRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "systemd", "user", "cicl-backup-c0.service")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("workspace root not found from %s", dir)
		}
		dir = parent
	}
}
