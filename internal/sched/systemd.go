package sched

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const managedUnitPrefix = "sched-command-"

var nonTokenChars = regexp.MustCompile(`[^a-z0-9]+`)

type SystemdOptions struct {
	UnitDir      string
	StateRoot    string
	OpenCodeHome string
	SchedBin     string
	SystemctlBin string
	Apply        bool
	DryRun       bool
}

type UnitPlan struct {
	ScheduleID     string `json:"scheduleId"`
	BaseName       string `json:"baseName"`
	ServiceName    string `json:"serviceName"`
	TimerName      string `json:"timerName"`
	ServicePath    string `json:"servicePath"`
	TimerPath      string `json:"timerPath"`
	ServiceContent string `json:"serviceContent,omitempty"`
	TimerContent   string `json:"timerContent,omitempty"`
}

type UnitSummary struct {
	ScheduleID       string   `json:"scheduleId"`
	ServiceName      string   `json:"serviceName"`
	TimerName        string   `json:"timerName"`
	ScheduleKind     string   `json:"scheduleKind"`
	Calendar         []string `json:"calendar,omitempty"`
	IntervalDuration string   `json:"intervalDuration,omitempty"`
}

type ReconcileResult struct {
	Units    []UnitSummary `json:"units"`
	Removed  []string      `json:"removed"`
	Warnings []string      `json:"warnings,omitempty"`
	DryRun   bool          `json:"dryRun"`
}

type StopResult struct {
	ScheduleID  string   `json:"scheduleId"`
	ServiceName string   `json:"serviceName"`
	Stopped     bool     `json:"stopped"`
	Message     string   `json:"message,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

func ReconcileSystemd(store *Store, opts SystemdOptions) (ReconcileResult, error) {
	if store == nil {
		return ReconcileResult{}, Errorf("invalid_state_root", "store is required")
	}
	if err := store.Ensure(); err != nil {
		return ReconcileResult{}, err
	}
	opts = normalizeSystemdOptions(store, opts)
	if err := os.MkdirAll(opts.UnitDir, 0o755); err != nil {
		return ReconcileResult{}, WrapError("systemd_write_failed", err, "failed to create systemd unit directory %s", opts.UnitDir)
	}
	if opts.OpenCodeHome != "" {
		if err := os.MkdirAll(filepath.Join(opts.OpenCodeHome, "log"), 0o755); err != nil {
			return ReconcileResult{}, WrapError("systemd_write_failed", err, "failed to create OpenCode log directory")
		}
	}

	jobs, err := store.ListJobs()
	if err != nil {
		return ReconcileResult{}, err
	}
	desired := map[string]UnitPlan{}
	result := ReconcileResult{Units: []UnitSummary{}, Removed: []string{}, DryRun: opts.DryRun}
	for _, job := range jobs {
		if job.Status != StatusActive {
			continue
		}
		if job.TimerSuppressed {
			reason := strings.TrimSpace(job.SuppressionReason)
			if reason == "" {
				reason = "timer suppressed"
			}
			result.Warnings = append(result.Warnings, fmt.Sprintf("suppressed %s: %s", job.ScheduleID, reason))
			continue
		}
		if strings.TrimSpace(job.AgentName) == "" {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skipped %s: agentName is blank", job.ScheduleID))
			continue
		}
		if strings.TrimSpace(job.CommandName) == "" {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skipped %s: commandName is blank", job.ScheduleID))
			continue
		}
		plan, calendars, err := RenderSystemdUnit(job, opts)
		if err != nil {
			return ReconcileResult{}, err
		}
		desired[plan.ServiceName] = plan
		desired[plan.TimerName] = plan
		summary := UnitSummary{ScheduleID: job.ScheduleID, ServiceName: plan.ServiceName, TimerName: plan.TimerName, ScheduleKind: job.ScheduleKind}
		if summary.ScheduleKind == ScheduleKindInterval {
			summary.IntervalDuration = job.IntervalDuration
		} else {
			summary.ScheduleKind = ScheduleKindCron
			summary.Calendar = calendars
		}
		result.Units = append(result.Units, summary)
		if !opts.DryRun {
			if err := writeFileAtomic(plan.ServicePath, []byte(plan.ServiceContent), 0o644); err != nil {
				return ReconcileResult{}, WrapError("systemd_write_failed", err, "failed to write %s", plan.ServicePath)
			}
			if err := writeFileAtomic(plan.TimerPath, []byte(plan.TimerContent), 0o644); err != nil {
				return ReconcileResult{}, WrapError("systemd_write_failed", err, "failed to write %s", plan.TimerPath)
			}
		}
	}

	existing, err := managedUnitFiles(opts.UnitDir)
	if err != nil {
		return ReconcileResult{}, err
	}
	for _, path := range existing {
		name := filepath.Base(path)
		if _, ok := desired[name]; ok {
			continue
		}
		result.Removed = append(result.Removed, name)
		if !opts.DryRun {
			if strings.HasSuffix(name, ".timer") {
				_ = systemctl(opts, "disable", "--now", name)
				_ = systemctl(opts, "stop", strings.TrimSuffix(name, ".timer")+".service")
			}
			if strings.HasSuffix(name, ".service") {
				_ = systemctl(opts, "stop", name)
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return ReconcileResult{}, WrapError("systemd_write_failed", err, "failed to remove stale unit %s", path)
			}
		}
	}
	sort.Strings(result.Removed)
	sort.Slice(result.Units, func(i, j int) bool { return result.Units[i].TimerName < result.Units[j].TimerName })

	if opts.Apply && !opts.DryRun {
		if err := systemctl(opts, "daemon-reload"); err != nil {
			return ReconcileResult{}, err
		}
		for _, unit := range result.Units {
			if err := systemctl(opts, "enable", "--now", unit.TimerName); err != nil {
				return ReconcileResult{}, err
			}
		}
	}
	return result, nil
}

func StopSystemdJob(store *Store, scheduleID string, opts SystemdOptions) (StopResult, error) {
	if store == nil {
		return StopResult{}, Errorf("invalid_state_root", "store is required")
	}
	if err := store.Ensure(); err != nil {
		return StopResult{}, err
	}
	job, err := store.GetJob(scheduleID)
	if err != nil {
		return StopResult{}, err
	}
	opts = normalizeSystemdOptions(store, opts)
	serviceName := UnitBaseName(job) + ".service"
	result := StopResult{ScheduleID: job.ScheduleID, ServiceName: serviceName}
	if opts.DryRun {
		result.Message = "dry run; service was not stopped"
		return result, nil
	}
	if !opts.Apply {
		result.Message = "systemctl disabled; service stop was not attempted"
		return result, nil
	}
	if err := systemctl(opts, "stop", serviceName); err != nil {
		message := ErrorMessage(err)
		lower := strings.ToLower(message)
		if strings.Contains(lower, "not loaded") || strings.Contains(lower, "could not be found") || strings.Contains(lower, "not-found") {
			result.Message = "service is not loaded or not running"
			return result, nil
		}
		result.Warnings = append(result.Warnings, message)
		return result, nil
	}
	result.Stopped = true
	return result, nil
}

func RenderSystemdUnit(job Job, opts SystemdOptions) (UnitPlan, []string, error) {
	scheduleLines, calendars, err := timerScheduleLines(job)
	if err != nil {
		return UnitPlan{}, nil, err
	}
	opts = normalizeSystemdOptions(nil, opts)
	base := UnitBaseName(job)
	serviceName := base + ".service"
	timerName := base + ".timer"
	stateRoot := opts.StateRoot
	if stateRoot == "" {
		stateRoot = DefaultStateRoot(opts.OpenCodeHome)
	}
	schedBin := opts.SchedBin
	if schedBin == "" {
		schedBin = "sched"
	}
	opencodeLogDir := filepath.Join(opts.OpenCodeHome, "log")
	notifyEnvLines := ""
	if job.NotifyChannel != nil && job.NotifyTo != nil {
		notifyEnvLines = fmt.Sprintf(
			"Environment=SCHED_NOTIFY_CHANNEL=%s\nEnvironment=SCHED_NOTIFY_TO=%s\n",
			escapeUnitValue(*job.NotifyChannel),
			escapeUnitValue(*job.NotifyTo),
		)
	}
	runCommand := strings.Join([]string{
		shellQuote(schedBin),
		"--state-root", shellQuote(stateRoot),
		"--opencode-home", shellQuote(opts.OpenCodeHome),
		"run", shellQuote(job.ScheduleID),
		"--source", RunSourceScheduled,
	}, " ")
	service := fmt.Sprintf(`[Unit]
Description=ChatInfra sched command %s

[Service]
Type=oneshot
WorkingDirectory=%s
EnvironmentFile=-%s/.env
Environment=OPENCODE_LOG_DIR=%s
%sExecStart=/bin/sh -lc %s
`, escapeUnitValue(job.ScheduleID), escapeUnitValue(job.Workdir), escapeUnitValue(job.Workdir), escapeUnitValue(opencodeLogDir), notifyEnvLines, shellQuote("exec "+runCommand))
	timer := fmt.Sprintf(`[Unit]
Description=Timer for ChatInfra sched command %s

[Timer]
Unit=%s
%s
Persistent=false

[Install]
WantedBy=timers.target
`, escapeUnitValue(job.ScheduleID), serviceName, strings.Join(scheduleLines, "\n"))
	return UnitPlan{
		ScheduleID:     job.ScheduleID,
		BaseName:       base,
		ServiceName:    serviceName,
		TimerName:      timerName,
		ServicePath:    filepath.Join(opts.UnitDir, serviceName),
		TimerPath:      filepath.Join(opts.UnitDir, timerName),
		ServiceContent: service,
		TimerContent:   timer,
	}, calendars, nil
}

func timerScheduleLines(job Job) ([]string, []string, error) {
	switch normalizeScheduleKind(job.ScheduleKind) {
	case ScheduleKindCron:
		calendars, err := CronToSystemdCalendars(job.ScheduleExpression, job.Timezone)
		if err != nil {
			return nil, nil, err
		}
		lines := make([]string, 0, len(calendars))
		for _, calendar := range calendars {
			lines = append(lines, "OnCalendar="+calendar)
		}
		return lines, calendars, nil
	case ScheduleKindInterval:
		interval, err := NormalizeIntervalDuration(job.IntervalDuration)
		if err != nil {
			return nil, nil, err
		}
		return []string{"OnBootSec=" + interval, "OnUnitActiveSec=" + interval}, nil, nil
	default:
		return nil, nil, Errorf("invalid_schedule_kind", "scheduleKind must be %q or %q", ScheduleKindCron, ScheduleKindInterval)
	}
}

func UnitBaseName(job Job) string {
	parts := []string{
		managedUnitPrefix + shortHash(job.OpenCodeID),
		token(job.AgentName, "agent"),
		token(job.CommandName, "command"),
		shortHash(job.ScheduleID),
	}
	return strings.Join(parts, "-")
}

func normalizeSystemdOptions(store *Store, opts SystemdOptions) SystemdOptions {
	if opts.OpenCodeHome == "" {
		if home, err := DefaultOpenCodeHome(os.Getenv); err == nil {
			opts.OpenCodeHome = home
		}
	}
	if opts.StateRoot == "" && store != nil {
		opts.StateRoot = store.Root
	}
	if opts.StateRoot == "" && opts.OpenCodeHome != "" {
		opts.StateRoot = DefaultStateRoot(opts.OpenCodeHome)
	}
	if opts.UnitDir == "" && opts.OpenCodeHome != "" {
		opts.UnitDir = DefaultSystemdUnitDir(opts.OpenCodeHome)
	}
	if opts.SchedBin == "" {
		if exe, err := os.Executable(); err == nil && exe != "" {
			opts.SchedBin = exe
		} else {
			opts.SchedBin = "sched"
		}
	}
	if opts.SystemctlBin == "" {
		opts.SystemctlBin = "systemctl"
	}
	return opts
}

func managedUnitFiles(unitDir string) ([]string, error) {
	entries, err := os.ReadDir(unitDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, WrapError("systemd_read_failed", err, "failed to read systemd unit directory %s", unitDir)
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, managedUnitPrefix) && (strings.HasSuffix(name, ".service") || strings.HasSuffix(name, ".timer")) {
			paths = append(paths, filepath.Join(unitDir, name))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func systemctl(opts SystemdOptions, args ...string) error {
	if !opts.Apply {
		return nil
	}
	cmd := exec.Command(opts.SystemctlBin, append([]string{"--user"}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return Errorf("systemd_command_failed", "systemctl --user %s failed: %s", strings.Join(args, " "), message)
	}
	return nil
}

func token(raw, fallback string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = nonTokenChars.ReplaceAllString(raw, "-")
	raw = strings.Trim(raw, "-")
	if raw == "" {
		raw = fallback
	}
	if len(raw) > 40 {
		raw = strings.Trim(raw[:40], "-")
	}
	if raw == "" {
		return fallback
	}
	return raw
}

func shortHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])[:8]
}

func shellQuote(raw string) string {
	return "'" + strings.ReplaceAll(raw, "'", "'\\''") + "'"
}

func escapeUnitValue(raw string) string {
	return strings.ReplaceAll(raw, "\n", " ")
}
