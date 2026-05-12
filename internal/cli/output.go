package cli

import (
	"strings"
	"time"

	"github.com/chatinfra/sched/internal/sched"
)

type jobSummary struct {
	ID       string `json:"id"`
	Action   string `json:"action,omitempty"`
	Status   string `json:"status"`
	Schedule string `json:"schedule"`
	Target   string `json:"target"`
	Workdir  string `json:"workdir,omitempty"`
	Last     string `json:"last,omitempty"`
}

type listResponse struct {
	Jobs []jobSummary `json:"jobs"`
}

type runSummary struct {
	ID         string    `json:"id"`
	ScheduleID string    `json:"scheduleId"`
	Source     string    `json:"source"`
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"startedAt"`
	DurationMs *int64    `json:"durationMs,omitempty"`
	ExitCode   *int      `json:"exitCode,omitempty"`
	Message    string    `json:"message,omitempty"`
}

type historyResponse struct {
	ScheduleID string       `json:"scheduleId,omitempty"`
	Runs       []runSummary `json:"runs"`
}

type reconcileSummary struct {
	Units    []reconcileUnitSummary `json:"units"`
	Removed  []string               `json:"removed"`
	Warnings []string               `json:"warnings,omitempty"`
	DryRun   bool                   `json:"dryRun"`
}

type reconcileUnitSummary struct {
	ID       string `json:"id"`
	Service  string `json:"service"`
	Timer    string `json:"timer"`
	Schedule string `json:"schedule"`
}

func compactJob(job sched.Job, action string, includeWorkdir bool) jobSummary {
	summary := jobSummary{
		ID:       job.ScheduleID,
		Action:   strings.TrimSpace(action),
		Status:   job.Status,
		Schedule: scheduleSummary(job),
		Target:   targetSummary(job),
	}
	if includeWorkdir {
		summary.Workdir = job.Workdir
	}
	if last := lastRunSummary(job); last != "" {
		summary.Last = last
	}
	return summary
}

func compactJobs(jobs []sched.Job) []jobSummary {
	summaries := make([]jobSummary, 0, len(jobs))
	for _, job := range jobs {
		summaries = append(summaries, compactJob(job, "", false))
	}
	return summaries
}

func compactRun(record sched.RunRecord) runSummary {
	summary := runSummary{
		ID:         record.RunID,
		ScheduleID: record.ScheduleID,
		Source:     record.Source,
		Status:     record.Status,
		StartedAt:  record.StartedAt,
		ExitCode:   record.ExitCode,
		Message:    record.Message,
	}
	if record.DurationMs > 0 {
		duration := record.DurationMs
		summary.DurationMs = &duration
	}
	return summary
}

func compactRuns(records []sched.RunRecord) []runSummary {
	summaries := make([]runSummary, 0, len(records))
	for _, record := range records {
		summaries = append(summaries, compactRun(record))
	}
	return summaries
}

func compactReconcile(result sched.ReconcileResult) reconcileSummary {
	units := make([]reconcileUnitSummary, 0, len(result.Units))
	for _, unit := range result.Units {
		units = append(units, reconcileUnitSummary{
			ID:       unit.ScheduleID,
			Service:  unit.ServiceName,
			Timer:    unit.TimerName,
			Schedule: unitScheduleSummary(unit),
		})
	}
	return reconcileSummary{
		Units:    units,
		Removed:  result.Removed,
		Warnings: result.Warnings,
		DryRun:   result.DryRun,
	}
}

func scheduleSummary(job sched.Job) string {
	switch job.ScheduleKind {
	case sched.ScheduleKindInterval:
		if strings.TrimSpace(job.IntervalDuration) == "" {
			return "interval"
		}
		return "every " + strings.TrimSpace(job.IntervalDuration)
	case sched.ScheduleKindCron:
		parts := []string{"cron"}
		if expr := strings.TrimSpace(job.ScheduleExpression); expr != "" {
			parts = append(parts, expr)
		}
		if tz := strings.TrimSpace(job.Timezone); tz != "" {
			parts = append(parts, tz)
		}
		return strings.Join(parts, " ")
	default:
		return strings.TrimSpace(job.ScheduleKind)
	}
}

func unitScheduleSummary(unit sched.UnitSummary) string {
	if strings.TrimSpace(unit.IntervalDuration) != "" {
		return "every " + strings.TrimSpace(unit.IntervalDuration)
	}
	if len(unit.Calendar) > 0 {
		return "cron " + strings.Join(unit.Calendar, "; ")
	}
	return strings.TrimSpace(unit.ScheduleKind)
}

func targetSummary(job sched.Job) string {
	command := strings.TrimSpace(job.CommandName)
	agent := strings.TrimSpace(job.AgentName)
	switch {
	case command != "" && agent != "":
		return command + " @ " + agent
	case command != "":
		return command
	case agent != "":
		return "@ " + agent
	default:
		return "<none>"
	}
}

func lastRunSummary(job sched.Job) string {
	status := strings.TrimSpace(job.LastRunStatus)
	if status == "" {
		return ""
	}
	if job.LastRunAt == nil || job.LastRunAt.IsZero() {
		return status
	}
	return status + " " + job.LastRunAt.UTC().Format(time.RFC3339Nano)
}
