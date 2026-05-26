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
