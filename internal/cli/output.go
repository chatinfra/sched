package cli

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chatinfra/sched/internal/sched"
)

type summaryResponse struct {
	Summary string `json:"summary"`
}

type listResponse struct {
	Jobs string `json:"jobs"`
}

type historyResponse struct {
	ScheduleID string `json:"scheduleId,omitempty"`
	Runs       string `json:"runs"`
}

type reconcileSummary struct {
	DryRun   bool   `json:"dryRun"`
	Units    string `json:"units"`
	Removed  string `json:"removed"`
	Warnings string `json:"warnings"`
}

func jobSummaryResponse(job sched.Job, action string, includeWorkdir bool) summaryResponse {
	parts := []string{}
	if action = cleanSummaryText(action); action != "" {
		parts = append(parts, action)
	}
	parts = append(parts, cleanSummaryText(job.ScheduleID), cleanSummaryText(job.Status), cleanSummaryText(scheduleSummary(job)), cleanSummaryText(targetSummary(job)))
	if includeWorkdir {
		parts = append(parts, "workdir="+cleanSummaryText(job.Workdir))
	}
	if last := lastRunSummary(job); last != "" {
		parts = append(parts, "last="+cleanSummaryText(last))
	}
	return summaryResponse{Summary: strings.Join(nonEmpty(parts), " ")}
}

func listJobsResponse(jobs []sched.Job) listResponse {
	rows := make([][]string, 0, len(jobs))
	for _, job := range jobs {
		last := lastRunSummary(job)
		if last == "" {
			last = "-"
		}
		rows = append(rows, []string{job.ScheduleID, job.Status, scheduleSummary(job), targetSummary(job), last})
	}
	return listResponse{Jobs: renderTable([]string{"ID", "STATUS", "SCHEDULE", "TARGET", "LAST"}, rows, "none")}
}

func deleteSummaryResponse(scheduleID string, deleted bool) summaryResponse {
	id := cleanSummaryText(scheduleID)
	if deleted {
		return summaryResponse{Summary: "deleted " + id}
	}
	return summaryResponse{Summary: "not found " + id + "; no schedule removed"}
}

func runSummaryResponse(record sched.RunRecord) summaryResponse {
	parts := []string{"run", record.RunID, record.ScheduleID, record.Source, record.Status}
	if duration := runDurationSummary(record); duration != "" {
		parts = append(parts, "duration="+duration)
	}
	if record.ExitCode != nil {
		parts = append(parts, "exit="+strconv.Itoa(*record.ExitCode))
	}
	if message := cleanSummaryText(record.Message); message != "" {
		parts = append(parts, "message="+message)
	}
	return summaryResponse{Summary: strings.Join(nonEmptyClean(parts), " ")}
}

func stopSummaryResponse(result sched.StopResult) summaryResponse {
	id := cleanSummaryText(result.ScheduleID)
	message := cleanSummaryText(result.Message)
	summary := "not stopped " + id
	if result.Stopped {
		summary = "stopped " + id
	} else if message != "" {
		summary += ": " + message
	}
	if warnings := cleanJoined(result.Warnings); warnings != "" {
		summary += " warnings=" + warnings
	}
	return summaryResponse{Summary: summary}
}

func historyRunsResponse(scheduleID string, records []sched.RunRecord) historyResponse {
	rows := make([][]string, 0, len(records))
	for _, record := range records {
		rows = append(rows, []string{record.RunID, record.ScheduleID, record.Source, record.Status, runDurationCell(record), exitCodeCell(record.ExitCode)})
	}
	return historyResponse{ScheduleID: strings.TrimSpace(scheduleID), Runs: renderTable([]string{"RUN", "SCHEDULE", "SOURCE", "STATUS", "DURATION", "EXIT"}, rows, "none")}
}

func reconcileSummaryResponse(result sched.ReconcileResult) reconcileSummary {
	unitRows := make([][]string, 0, len(result.Units))
	for _, unit := range result.Units {
		unitRows = append(unitRows, []string{unit.ScheduleID, unit.TimerName, unit.ServiceName, unitScheduleSummary(unit)})
	}
	return reconcileSummary{
		DryRun:   result.DryRun,
		Units:    renderTable([]string{"ID", "TIMER", "SERVICE", "SCHEDULE"}, unitRows, "none"),
		Removed:  renderOneColumnTable("UNIT", result.Removed, "none"),
		Warnings: renderOneColumnTable("WARNING", result.Warnings, "none"),
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

func renderOneColumnTable(header string, values []string, empty string) string {
	rows := make([][]string, 0, len(values))
	for _, value := range values {
		rows = append(rows, []string{value})
	}
	return renderTable([]string{header}, rows, empty)
}

func renderTable(headers []string, rows [][]string, empty string) string {
	if len(rows) == 0 {
		return cleanSummaryText(empty)
	}
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = cellWidth(header)
	}
	for _, row := range rows {
		for i := range headers {
			width := cellWidth(cellAt(row, i))
			if width > widths[i] {
				widths[i] = width
			}
		}
	}
	var b strings.Builder
	appendTableRow(&b, headers, widths)
	for _, row := range rows {
		b.WriteByte('\n')
		appendTableRow(&b, row, widths)
	}
	return b.String()
}

func appendTableRow(b *strings.Builder, row []string, widths []int) {
	for i := range widths {
		if i > 0 {
			b.WriteString("  ")
		}
		cell := cleanCell(cellAt(row, i))
		b.WriteString(cell)
		for pad := widths[i] - cellWidth(cell); pad > 0; pad-- {
			b.WriteByte(' ')
		}
	}
}

func cellAt(row []string, index int) string {
	if index < len(row) {
		return row[index]
	}
	return ""
}

func cleanCell(value string) string {
	value = cleanSummaryText(value)
	if value == "" {
		return "-"
	}
	return value
}

func cleanSummaryText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func cleanJoined(values []string) string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		if value = cleanSummaryText(value); value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return strings.Join(cleaned, "; ")
}

func nonEmpty(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func nonEmptyClean(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if value = cleanSummaryText(value); value != "" {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func runDurationSummary(record sched.RunRecord) string {
	if record.FinishedAt == nil && record.DurationMs <= 0 {
		return ""
	}
	if record.DurationMs < 0 {
		return ""
	}
	return strconv.FormatInt(record.DurationMs, 10) + "ms"
}

func runDurationCell(record sched.RunRecord) string {
	if duration := runDurationSummary(record); duration != "" {
		return duration
	}
	return "-"
}

func exitCodeCell(exitCode *int) string {
	if exitCode == nil {
		return "-"
	}
	return strconv.Itoa(*exitCode)
}

func cellWidth(value string) int {
	return utf8.RuneCountInString(cleanCell(value))
}
