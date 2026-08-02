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
	"syscall"
)

const (
	managedUnitPrefix     = "sched-command-"
	superdirServeUnitBase = "superdir-serve"
)

var nonTokenChars = regexp.MustCompile(`[^a-z0-9]+`)

type SystemdOptions struct {
	UnitDir      string
	StateRoot    string
	OpenCodeHome string
	FromRepoDir  string
	RepoRoot     string
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
	opts = normalizeSystemdOptions(store, opts)
	var repoPlans []UnitPlan
	var repoSummaries []UnitSummary
	var repoWarnings []string
	if strings.TrimSpace(opts.FromRepoDir) != "" {
		var err error
		repoPlans, repoSummaries, repoWarnings, err = repoOperatorUnitPlans(opts)
		if err != nil {
			return ReconcileResult{}, err
		}
	}
	if err := store.Ensure(); err != nil {
		return ReconcileResult{}, err
	}
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
	plans := map[string]UnitPlan{}
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
		plans[plan.BaseName] = plan
		summary := UnitSummary{ScheduleID: job.ScheduleID, ServiceName: plan.ServiceName, TimerName: plan.TimerName, ScheduleKind: job.ScheduleKind}
		if summary.ScheduleKind == ScheduleKindInterval {
			summary.IntervalDuration = job.IntervalDuration
		} else {
			summary.ScheduleKind = ScheduleKindCron
			summary.Calendar = calendars
		}
		result.Units = append(result.Units, summary)
	}

	if strings.TrimSpace(opts.FromRepoDir) != "" {
		result.Warnings = append(result.Warnings, repoWarnings...)
		for i, plan := range repoPlans {
			if _, ok := desired[plan.ServiceName]; ok {
				return ReconcileResult{}, Errorf("duplicate_systemd_unit", "systemd unit %s is produced more than once", plan.ServiceName)
			}
			if _, ok := desired[plan.TimerName]; ok {
				return ReconcileResult{}, Errorf("duplicate_systemd_unit", "systemd unit %s is produced more than once", plan.TimerName)
			}
			desired[plan.ServiceName] = plan
			desired[plan.TimerName] = plan
			plans[plan.BaseName] = plan
			result.Units = append(result.Units, repoSummaries[i])
		}
	}

	if !opts.DryRun {
		writePlans := make([]UnitPlan, 0, len(plans))
		for _, plan := range plans {
			writePlans = append(writePlans, plan)
		}
		sort.Slice(writePlans, func(i, j int) bool { return writePlans[i].TimerName < writePlans[j].TimerName })
		for _, plan := range writePlans {
			if err := writeFileAtomic(plan.ServicePath, []byte(plan.ServiceContent), 0o644); err != nil {
				return ReconcileResult{}, WrapError("systemd_write_failed", err, "failed to write %s", plan.ServicePath)
			}
			if err := writeFileAtomic(plan.TimerPath, []byte(plan.TimerContent), 0o644); err != nil {
				return ReconcileResult{}, WrapError("systemd_write_failed", err, "failed to write %s", plan.TimerPath)
			}
		}
	}

	existing, err := pruneCandidateUnitFiles(opts)
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

type xSchedMetadata struct {
	Present bool
	Managed bool
	ID      string
}

func repoOperatorUnitPlans(opts SystemdOptions) ([]UnitPlan, []UnitSummary, []string, error) {
	repoUnitDir := strings.TrimSpace(opts.FromRepoDir)
	if repoUnitDir == "" {
		return nil, nil, nil, nil
	}
	absDir, err := filepath.Abs(repoUnitDir)
	if err != nil {
		return nil, nil, nil, WrapError("systemd_read_failed", err, "failed to resolve repo unit directory %s", repoUnitDir)
	}
	repoRoot := strings.TrimSpace(opts.RepoRoot)
	if repoRoot == "" {
		return nil, nil, nil, Errorf("missing_repo_root", "--repo-root is required with --from-repo")
	}
	if !filepath.IsAbs(repoRoot) {
		return nil, nil, nil, Errorf("invalid_repo_root", "--repo-root must be an absolute path")
	}
	repoRoot = filepath.Clean(repoRoot)
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, nil, nil, WrapError("systemd_read_failed", err, "failed to read repo unit directory %s", absDir)
	}
	bases := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, ".service"):
			bases[strings.TrimSuffix(name, ".service")] = true
		case strings.HasSuffix(name, ".timer"):
			bases[strings.TrimSuffix(name, ".timer")] = true
		}
	}
	sortedBases := make([]string, 0, len(bases))
	for base := range bases {
		sortedBases = append(sortedBases, base)
	}
	sort.Strings(sortedBases)

	var plans []UnitPlan
	var summaries []UnitSummary
	var warnings []string
	for _, base := range sortedBases {
		servicePath := filepath.Join(absDir, base+".service")
		timerPath := filepath.Join(absDir, base+".timer")
		serviceData, serviceErr := os.ReadFile(servicePath)
		timerData, timerErr := os.ReadFile(timerPath)
		serviceMeta := parseXSchedSection(string(serviceData))
		timerMeta := parseXSchedSection(string(timerData))
		if isReservedOperatorUnitBase(base) && (serviceMeta.Managed || timerMeta.Managed) {
			return nil, nil, nil, Errorf("reserved_operator_unit_name", "operator unit %s uses a reserved unit name", base)
		}
		if serviceErr != nil || timerErr != nil {
			warnings = append(warnings, fmt.Sprintf("ignored %s: missing .service/.timer pair", base))
			continue
		}
		if !serviceMeta.Managed && !timerMeta.Managed {
			warnings = append(warnings, fmt.Sprintf("ignored %s: missing [X-Sched] Managed=true", base))
			continue
		}
		if !serviceMeta.Managed || !timerMeta.Managed {
			warnings = append(warnings, fmt.Sprintf("ignored %s: both service and timer must declare [X-Sched] Managed=true", base))
			continue
		}
		id := strings.TrimSpace(serviceMeta.ID)
		if id == "" || strings.TrimSpace(timerMeta.ID) == "" {
			return nil, nil, nil, Errorf("invalid_x_sched_unit", "operator unit %s must set [X-Sched] Id in both service and timer", base)
		}
		if id != strings.TrimSpace(timerMeta.ID) {
			return nil, nil, nil, Errorf("invalid_x_sched_unit", "operator unit %s has mismatched [X-Sched] Id values", base)
		}
		serviceName := base + ".service"
		timerName := base + ".timer"
		renderedService := strings.ReplaceAll(string(serviceData), "@REPO_ROOT@", repoRoot)
		renderedTimer := strings.ReplaceAll(string(timerData), "@REPO_ROOT@", repoRoot)
		if err := validateOperatorExecStarts(serviceName, renderedService, repoRoot); err != nil {
			return nil, nil, nil, err
		}
		plan := UnitPlan{
			ScheduleID:     id,
			BaseName:       base,
			ServiceName:    serviceName,
			TimerName:      timerName,
			ServicePath:    filepath.Join(opts.UnitDir, serviceName),
			TimerPath:      filepath.Join(opts.UnitDir, timerName),
			ServiceContent: renderedService,
			TimerContent:   renderedTimer,
		}
		plans = append(plans, plan)
		summaries = append(summaries, repoUnitSummary(plan, renderedTimer))
	}
	return plans, summaries, warnings, nil
}

func repoUnitSummary(plan UnitPlan, timerContent string) UnitSummary {
	summary := UnitSummary{ScheduleID: plan.ScheduleID, ServiceName: plan.ServiceName, TimerName: plan.TimerName, ScheduleKind: "operator"}
	if calendars := timerSectionValues(timerContent, "OnCalendar"); len(calendars) > 0 {
		summary.ScheduleKind = ScheduleKindCron
		summary.Calendar = calendars
		return summary
	}
	if intervals := timerSectionValues(timerContent, "OnUnitActiveSec"); len(intervals) > 0 {
		summary.ScheduleKind = ScheduleKindInterval
		summary.IntervalDuration = intervals[0]
		return summary
	}
	if intervals := timerSectionValues(timerContent, "OnBootSec"); len(intervals) > 0 {
		summary.ScheduleKind = ScheduleKindInterval
		summary.IntervalDuration = intervals[0]
	}
	return summary
}

func validateOperatorExecStarts(unitName, content, repoRoot string) error {
	execStarts := serviceSectionValues(content, "ExecStart")
	if len(execStarts) == 0 {
		return Errorf("invalid_exec_start", "operator unit %s has no ExecStart", unitName)
	}
	for _, command := range execStarts {
		execPath := execStartPath(command)
		if execPath == "" || !filepath.IsAbs(execPath) {
			return Errorf("invalid_exec_start", "operator unit %s has invalid ExecStart executable %q", unitName, execPath)
		}
		execPath = filepath.Clean(execPath)
		if pathWithin(execPath, repoRoot) {
			return Errorf("repo_contained_exec_start", "operator unit %s ExecStart executable %s is inside repository root %s", unitName, execPath, repoRoot)
		}
		info, err := os.Stat(execPath)
		if err != nil {
			return Errorf("invalid_exec_start", "operator unit %s ExecStart executable %s is not available: %v", unitName, execPath, err)
		}
		resolvedPath, err := filepath.EvalSymlinks(execPath)
		if err != nil {
			return Errorf("invalid_exec_start", "operator unit %s ExecStart executable %s cannot be resolved: %v", unitName, execPath, err)
		}
		resolvedRoot := repoRoot
		if resolved, err := filepath.EvalSymlinks(repoRoot); err == nil {
			resolvedRoot = resolved
		}
		if pathWithin(resolvedPath, resolvedRoot) {
			return Errorf("repo_contained_exec_start", "operator unit %s ExecStart executable %s resolves inside repository root %s", unitName, execPath, repoRoot)
		}
		if !info.Mode().IsRegular() || syscall.Access(execPath, 1) != nil {
			return Errorf("invalid_exec_start", "operator unit %s ExecStart executable %s is not executable by the invoking user", unitName, execPath)
		}
	}
	return nil
}

func serviceSectionValues(content, key string) []string {
	var values []string
	inService := false
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			end := strings.Index(line, "]")
			inService = end >= 0 && strings.EqualFold(strings.TrimSpace(line[1:end]), "Service")
			continue
		}
		if !inService {
			continue
		}
		field, value, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(field), key) && strings.TrimSpace(value) != "" {
			values = append(values, strings.TrimSpace(value))
		}
	}
	return values
}

func execStartPath(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(strings.TrimLeft(fields[0], "-@:+!"), "\"'")
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func parseXSchedSection(content string) xSchedMetadata {
	metadata := xSchedMetadata{}
	inSection := false
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			end := strings.Index(line, "]")
			if end < 0 {
				inSection = false
				continue
			}
			section := strings.TrimSpace(line[1:end])
			inSection = strings.EqualFold(section, "X-Sched")
			if inSection {
				metadata.Present = true
			}
			continue
		}
		if !inSection {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "managed":
			metadata.Managed = parseUnitBool(value)
		case "id":
			metadata.ID = unitMetadataValue(value)
		}
	}
	return metadata
}

func parseUnitBool(raw string) bool {
	switch strings.ToLower(unitMetadataValue(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func unitMetadataValue(raw string) string {
	value := strings.TrimSpace(raw)
	for _, marker := range []string{" #", "\t#", " ;", "\t;"} {
		if index := strings.Index(value, marker); index >= 0 {
			value = value[:index]
		}
	}
	return strings.TrimSpace(value)
}

func timerSectionValues(content string, key string) []string {
	var values []string
	inTimer := false
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			end := strings.Index(line, "]")
			if end < 0 {
				inTimer = false
				continue
			}
			inTimer = strings.EqualFold(strings.TrimSpace(line[1:end]), "Timer")
			continue
		}
		if !inTimer {
			continue
		}
		field, value, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(field), key) {
			values = append(values, unitMetadataValue(value))
		}
	}
	return values
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

func pruneCandidateUnitFiles(opts SystemdOptions) ([]string, error) {
	if strings.TrimSpace(opts.FromRepoDir) != "" {
		return managedOperatorUnitFiles(opts.UnitDir)
	}
	return managedUnitFiles(opts.UnitDir)
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

func managedOperatorUnitFiles(unitDir string) ([]string, error) {
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
		base, isUnit := systemdUnitBase(name)
		if !isUnit || isReservedOperatorUnitBase(base) {
			continue
		}
		path := filepath.Join(unitDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, WrapError("systemd_read_failed", err, "failed to read installed systemd unit %s", path)
		}
		if parseXSchedSection(string(data)).Managed {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func isReservedOperatorUnitBase(base string) bool {
	return strings.HasPrefix(base, managedUnitPrefix) || base == superdirServeUnitBase
}

func systemdUnitBase(name string) (string, bool) {
	switch {
	case strings.HasSuffix(name, ".service"):
		return strings.TrimSuffix(name, ".service"), true
	case strings.HasSuffix(name, ".timer"):
		return strings.TrimSuffix(name, ".timer"), true
	default:
		return "", false
	}
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
