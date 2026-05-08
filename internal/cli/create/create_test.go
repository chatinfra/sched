package create

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chatinfra/sched/internal/cli/clitest"
	"github.com/chatinfra/sched/internal/sched"
)

func TestCreateIntervalCommandYAMLPersistsAndReconciles(t *testing.T) {
	h := clitest.New(t)
	workdir := filepath.Join(h.Root, "work")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workdir) error = %v", err)
	}

	result := h.Run(t, "create", "--command", "hello", "--agent", "syslog", "--every", "5s", "--workdir", workdir).RequireSuccess(t)
	clitest.RequireYAMLStdout(t, result, "create.schema.yaml")
	job := clitest.DecodeYAMLAs[sched.Job](t, result.Stdout)

	if job.ScheduleKind != sched.ScheduleKindInterval || job.IntervalDuration != "5s" {
		t.Fatalf("interval metadata = kind %q duration %q", job.ScheduleKind, job.IntervalDuration)
	}
	if job.CommandName != "hello" || job.AgentName != "syslog" || job.Workdir != workdir {
		t.Fatalf("created job metadata = %#v", job)
	}
	if job.TenantID != "local" || job.OpenCodeID != "local" || job.CommandID != "hello" || job.AgentID != "syslog" {
		t.Fatalf("derived local metadata = %#v", job)
	}
	if job.ScheduleExpression != "" || job.Timezone != "" {
		t.Fatalf("interval job should not require cron fields: %#v", job)
	}
	if job.Title != sched.ExpectedTitle(job.CommandID, job.ScheduleID) {
		t.Fatalf("title = %q", job.Title)
	}
	if _, err := os.Stat(h.JobPath(job.ScheduleID)); err != nil {
		t.Fatalf("persisted job missing: %v", err)
	}

	service, timer := readRenderedUnits(t, h.SystemdDir)
	for _, want := range []string{"OnBootSec=5s", "OnUnitActiveSec=5s", "Persistent=false"} {
		if !strings.Contains(timer, want) {
			t.Fatalf("timer missing %q:\n%s", want, timer)
		}
	}
	if strings.Contains(timer, "OnCalendar=") {
		t.Fatalf("interval timer should not contain OnCalendar:\n%s", timer)
	}
	for _, want := range []string{"run", job.ScheduleID, "--source scheduled"} {
		if !strings.Contains(service, want) {
			t.Fatalf("service missing %q:\n%s", want, service)
		}
	}
	if strings.Contains(service, "--json") {
		t.Fatalf("service should not request JSON output:\n%s", service)
	}
	if strings.Contains(service, "opencode run") {
		t.Fatalf("service should run through sched envelope, not opencode directly:\n%s", service)
	}
}

func TestCreateRejectsInvalidInputWithoutPartialWrites(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		code       string
		message    string
		argument   string
		received   string
		suggestion string
		example    string
	}{
		{
			name:    "missing command",
			args:    []string{"create", "--agent", "syslog", "--every", "5s"},
			code:    "missing_required_flag",
			message: "--command is required",
		},
		{
			name:    "missing agent",
			args:    []string{"create", "--command", "hello", "--every", "5s"},
			code:    "missing_required_flag",
			message: "--agent is required",
		},
		{
			name:    "missing every",
			args:    []string{"create", "--command", "hello", "--agent", "syslog"},
			code:    "missing_required_flag",
			message: "--every is required",
		},
		{
			name:    "missing workdir",
			args:    []string{"create", "--command", "hello", "--agent", "syslog", "--every", "5s"},
			code:    "missing_required_flag",
			message: "--workdir is required",
		},
		{
			name:    "invalid interval",
			args:    []string{"create", "--command", "hello", "--agent", "syslog", "--every", "0s"},
			code:    "invalid_interval",
			message: "interval duration must be positive",
		},
		{
			name:       "quoted natural language interval",
			args:       []string{"create", "--command", "hello", "--agent", "syslog", "--every", "1 minute"},
			code:       "invalid_interval",
			message:    `interval duration "1 minute" is invalid`,
			argument:   "--every",
			received:   "1 minute",
			suggestion: "--every 1m",
			example:    "--every 1m",
		},
		{
			name:       "split natural language interval",
			args:       []string{"create", "--command", "hello", "--agent", "syslog", "--every", "1", "minute"},
			code:       "invalid_interval",
			message:    `interval duration "1 minute" is invalid`,
			argument:   "--every",
			received:   "1 minute",
			suggestion: "--every 1m",
			example:    "--every 1m",
		},
		{
			name:    "unsafe schedule id",
			args:    []string{"create", "--command", "hello", "--agent", "syslog", "--every", "5s", "--schedule-id", "../bad"},
			code:    "invalid_schedule_id",
			message: "not a safe identifier",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			h := clitest.New(t)
			workdir := filepath.Join(h.Root, "work")
			if err := os.MkdirAll(workdir, 0o755); err != nil {
				t.Fatalf("MkdirAll(workdir) error = %v", err)
			}
			args := append([]string{}, tt.args...)
			if tt.name != "missing workdir" {
				args = append(args, "--workdir", workdir)
			}

			result := h.Run(t, args...).RequireError(t)
			clitest.RequireYAMLError(t, result, "error.schema.yaml")
			envelope := clitest.DecodeYAMLAs[clitest.ErrorEnvelope](t, result.Stderr)
			if envelope.Error.Code != tt.code || !strings.Contains(envelope.Error.Message, tt.message) {
				t.Fatalf("error envelope = %#v, want code %q message containing %q", envelope.Error, tt.code, tt.message)
			}
			if tt.argument != "" {
				if envelope.Error.Argument != tt.argument || envelope.Error.Received != tt.received || envelope.Error.Expected == "" || envelope.Error.Suggestion != tt.suggestion || !examplesContain(envelope.Error.Examples, tt.example) {
					t.Fatalf("diagnostic error = %#v", envelope.Error)
				}
				if !strings.Contains(envelope.Error.Expected, "Go-style duration") {
					t.Fatalf("expected guidance = %q", envelope.Error.Expected)
				}
			}
			assertNoPartialState(t, h)
		})
	}
}

func TestCreateSplitIntervalYAMLErrorIncludesHint(t *testing.T) {
	h := clitest.New(t)
	workdir := filepath.Join(h.Root, "work")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workdir) error = %v", err)
	}

	result := h.Run(t, "create", "--command", "hello", "--agent", "syslog", "--every", "1", "minute", "--workdir", workdir).RequireError(t)
	clitest.RequireYAMLError(t, result, "error.schema.yaml")
	envelope := clitest.DecodeYAMLAs[clitest.ErrorEnvelope](t, result.Stderr)
	if !strings.Contains(envelope.Error.Message, `interval duration "1 minute" is invalid`) {
		t.Fatalf("stderr missing primary message: %#v", envelope.Error)
	}
	if envelope.Error.Hint == "" || (!strings.Contains(strings.Join(envelope.Error.Examples, " "), "--every 1m") && !strings.Contains(strings.Join(envelope.Error.Examples, " "), "--every 60s")) {
		t.Fatalf("stderr missing correction hint/example: %#v", envelope.Error)
	}
	assertNoPartialState(t, h)
}

func examplesContain(examples []string, want string) bool {
	for _, example := range examples {
		if example == want {
			return true
		}
	}
	return false
}

func readRenderedUnits(t *testing.T, unitDir string) (string, string) {
	t.Helper()
	entries, err := os.ReadDir(unitDir)
	if err != nil {
		t.Fatalf("ReadDir(systemd) error = %v", err)
	}
	var service string
	var timer string
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(unitDir, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", entry.Name(), err)
		}
		if strings.HasSuffix(entry.Name(), ".service") {
			service = string(data)
		}
		if strings.HasSuffix(entry.Name(), ".timer") {
			timer = string(data)
		}
	}
	if service == "" || timer == "" {
		t.Fatalf("expected rendered service and timer, got entries %#v", entries)
	}
	return service, timer
}

func assertNoPartialState(t *testing.T, h *clitest.Harness) {
	t.Helper()
	jobsDir := filepath.Join(h.StateRoot, "jobs")
	if entries, err := os.ReadDir(jobsDir); err == nil && len(entries) != 0 {
		t.Fatalf("partial job state written: %#v", entries)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadDir(jobs) error = %v", err)
	}
	entries, err := os.ReadDir(h.SystemdDir)
	if err != nil {
		t.Fatalf("ReadDir(systemd) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial systemd artifacts written: %#v", entries)
	}
}
