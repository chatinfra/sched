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

type terminalField struct {
	Label string
	Value string
}

func jobTerminalOutput(job sched.Job, action string, includeWorkdir bool) string {
	id := cleanCell(job.ScheduleID)
	fields := []terminalField{
		{Label: "Status", Value: job.Status},
		{Label: "Schedule", Value: scheduleSummary(job)},
		{Label: "Command", Value: job.CommandName},
		{Label: "Agent", Value: job.AgentName},
	}
	if includeWorkdir {
		fields = append(fields, terminalField{Label: "Workdir", Value: job.Workdir})
	}
	if last := lastRunSummary(job); last != "" {
		fields = append(fields, terminalField{Label: "Last run", Value: last})
	}
	if action = cleanSummaryText(action); action != "" {
		return capitalizeFirst(action) + " schedule " + id + "\n\n" + renderFieldBlock(fields)
	}
	return id + "\n" + renderFieldBlock(fields)
}

func listJobsOutput(jobs []sched.Job) string {
	rows := make([][]string, 0, len(jobs))
	for _, job := range jobs {
		last := lastRunSummary(job)
		if last == "" {
			last = "-"
		}
		rows = append(rows, []string{job.ScheduleID, job.Status, scheduleSummary(job), targetSummary(job), last})
	}
	return renderTable([]string{"ID", "STATUS", "SCHEDULE", "TARGET", "LAST"}, rows, "No schedules found.")
}

func deleteTerminalOutput(scheduleID string, deleted bool) string {
	id := cleanCell(scheduleID)
	if deleted {
		return "Deleted schedule " + id
	}
	return "No schedule removed for " + id + " (not found)."
}

func runTerminalOutput(record sched.RunRecord) string {
	title := "Run " + cleanCell(record.RunID)
	if scheduleID := cleanSummaryText(record.ScheduleID); scheduleID != "" {
		title += " for schedule " + scheduleID
	}
	fields := []terminalField{
		{Label: "Source", Value: record.Source},
		{Label: "Status", Value: record.Status},
		{Label: "Duration", Value: runDurationCell(record)},
		{Label: "Exit", Value: exitCodeCell(record.ExitCode)},
	}
	if message := cleanSummaryText(record.Message); message != "" {
		fields = append(fields, terminalField{Label: "Message", Value: message})
	}
	return title + "\n" + renderFieldBlock(fields)
}

func stopTerminalOutput(result sched.StopResult) string {
	id := cleanCell(result.ScheduleID)
	message := cleanSummaryText(result.Message)
	lines := []string{"No active work stopped for schedule " + id + "."}
	if result.Stopped {
		lines[0] = "Stopped schedule " + id + "."
	}
	fields := []terminalField{}
	if message != "" {
		fields = append(fields, terminalField{Label: "Message", Value: message})
	}
	if warnings := cleanJoined(result.Warnings); warnings != "" {
		fields = append(fields, terminalField{Label: "Warnings", Value: warnings})
	}
	if len(fields) > 0 {
		lines = append(lines, "", renderFieldBlock(fields))
	}
	return strings.Join(lines, "\n")
}

func historyRunsOutput(scheduleID string, records []sched.RunRecord) string {
	rows := make([][]string, 0, len(records))
	for _, record := range records {
		rows = append(rows, []string{record.RunID, record.ScheduleID, record.Source, record.Status, runDurationCell(record), exitCodeCell(record.ExitCode)})
	}
	empty := "No run history found."
	if scheduleID = cleanSummaryText(scheduleID); scheduleID != "" {
		empty = "No run history found for schedule " + scheduleID + "."
	}
	return renderTable([]string{"RUN", "SCHEDULE", "SOURCE", "STATUS", "DURATION", "EXIT"}, rows, empty)
}

func reconcileTerminalOutput(result sched.ReconcileResult) string {
	unitRows := make([][]string, 0, len(result.Units))
	for _, unit := range result.Units {
		unitRows = append(unitRows, []string{unit.ScheduleID, unit.TimerName, unit.ServiceName, unitScheduleSummary(unit)})
	}
	sections := []string{
		"Dry run: " + strconv.FormatBool(result.DryRun),
		"",
		"Reconciled units",
		renderTable([]string{"ID", "TIMER", "SERVICE", "SCHEDULE"}, unitRows, "No units reconciled."),
		"",
		"Removed units",
		renderOneColumnTable("UNIT", result.Removed, "No stale units removed."),
		"",
		"Warnings",
		renderOneColumnTable("WARNING", result.Warnings, "No warnings."),
	}
	return strings.Join(sections, "\n")
}

func rootHelpText(help map[string]any) string {
	title := helpString(help, "tool")
	if summary := helpString(help, "summary"); summary != "" {
		title += " - " + summary
	}
	sections := []string{title}
	if usage := helpString(help, "usage"); usage != "" {
		sections = append(sections, "USAGE\n  "+usage)
	}
	if rows := namedSummaryRows(help["commands"]); len(rows) > 0 {
		sections = append(sections, "COMMANDS\n"+indentLines(renderTable([]string{"COMMAND", "SUMMARY"}, rows, ""), "  "))
	}
	if rows := namedSummaryRows(help["flags"]); len(rows) > 0 {
		sections = append(sections, "FLAGS\n"+indentLines(renderTable([]string{"FLAG", "SUMMARY"}, rows, ""), "  "))
	}
	sections = append(sections, "OUTPUT\n"+indentLines(strings.Join(rootOutputLines(), "\n"), "  "))
	sections = append(sections, "EXAMPLES\n"+indentLines(strings.Join(rootHelpExamples(), "\n"), "  "))
	if seeAlso := rootSeeAlso(help); len(seeAlso) > 0 {
		sections = append(sections, "SEE ALSO\n"+indentLines(strings.Join(seeAlso, "\n"), "  "))
	}
	return strings.Join(sections, "\n\n")
}

func rootOutputLines() []string {
	return []string{
		"stdout: default operator commands emit concise terminal text; commands with --full emit structured YAML.",
		"stdout: sched export and sched schemas emit structured YAML for state transfer and schema discovery.",
		"stderr: errors emit the YAML error envelope and leave stdout empty.",
	}
}

func rootHelpExamples() []string {
	return []string{
		"sched create --command hello --agent syslog --every 5s --workdir /data/opencode/work",
		"sched list",
		"sched systemd reconcile --full",
		"sched systemd reconcile --from-repo systemd/user --repo-root /home/operator/super --dry-run",
	}
}

func rootSeeAlso(help map[string]any) []string {
	discovery := help["discovery"]
	seeAlso := []string{"sched help --full"}
	if commandHelp := helpMapString(discovery, "commandHelp"); commandHelp != "" {
		seeAlso = append(seeAlso, commandHelp)
	}
	if schemas := helpMapString(discovery, "schemas"); schemas != "" {
		seeAlso = append(seeAlso, schemas)
	}
	return seeAlso
}

func commandHelpText(help map[string]any) string {
	command := helpString(help, "command")
	title := "sched " + command
	if summary := helpString(help, "summary"); summary != "" {
		title += " - " + summary
	}
	sections := []string{title}
	if usage := helpString(help, "usage"); usage != "" {
		sections = append(sections, "USAGE\n  "+usage)
	}
	if rows := namedSummaryRows(help["flags"]); len(rows) > 0 {
		sections = append(sections, "FLAGS\n"+indentLines(renderTable([]string{"FLAG", "SUMMARY"}, rows, ""), "  "))
	}
	outputLines := []string{}
	if output := helpString(help, "output"); output != "" {
		outputLines = append(outputLines, output)
	}
	if schema := helpString(help, "schema"); schema != "" {
		outputLines = append(outputLines, "Schema: "+schema)
	}
	if fullSchema := helpString(help, "fullSchema"); fullSchema != "" {
		outputLines = append(outputLines, "Full schema: "+fullSchema)
	}
	outputLines = append(outputLines, "stderr: errors emit the YAML error envelope and leave stdout empty.")
	sections = append(sections, "OUTPUT\n"+indentLines(strings.Join(outputLines, "\n"), "  "))
	if examples := commandExamples(command); len(examples) > 0 {
		sections = append(sections, "EXAMPLES\n"+indentLines(strings.Join(examples, "\n"), "  "))
	}
	if seeAlso := commandSeeAlso(command); len(seeAlso) > 0 {
		sections = append(sections, "SEE ALSO\n"+indentLines(strings.Join(seeAlso, "\n"), "  "))
	}
	return strings.Join(sections, "\n\n")
}

func commandExamples(command string) []string {
	switch command {
	case "create":
		return []string{"sched create --command hello --agent syslog --every 5s --workdir /data/opencode/work"}
	case "put":
		return []string{"sched put --stdin --full < job.json"}
	case "put-many":
		return []string{"sched put-many --stdin --full < jobs.json"}
	case "get":
		return []string{"sched get sched-1", "sched get sched-1 --full"}
	case "list":
		return []string{"sched list", "sched list --full"}
	case "delete":
		return []string{"sched delete sched-1", "sched delete sched-1 --full"}
	case "run":
		return []string{"sched run sched-1 --source manual", "sched run sched-1 --source manual --full"}
	case "stop":
		return []string{"sched stop sched-1", "sched stop sched-1 --full"}
	case "history":
		return []string{"sched history --schedule-id sched-1 --limit 20", "sched history --limit 20 --full"}
	case "export":
		return []string{"sched export > sched-state.yaml"}
	case "systemd reconcile":
		return []string{"sched systemd reconcile", "sched systemd reconcile --from-repo systemd/user --repo-root /home/operator/super --dry-run", "sched systemd reconcile --full"}
	case "schemas":
		return []string{"sched schemas"}
	default:
		return nil
	}
}

func commandSeeAlso(command string) []string {
	if command == "" {
		return nil
	}
	return []string{"sched help " + command + " --full", "sched schemas"}
}

func namedSummaryRows(value any) [][]string {
	rows := [][]string{}
	appendItem := func(item map[string]any) {
		name := helpString(item, "name")
		if name == "" {
			name = helpString(item, "command")
		}
		summary := helpString(item, "summary")
		if name != "" || summary != "" {
			rows = append(rows, []string{name, summary})
		}
	}
	switch items := value.(type) {
	case []map[string]any:
		for _, item := range items {
			appendItem(item)
		}
	case []any:
		for _, raw := range items {
			if item, ok := raw.(map[string]any); ok {
				appendItem(item)
			}
		}
	}
	return rows
}

func helpString(item map[string]any, key string) string {
	value, _ := item[key].(string)
	return cleanSummaryText(value)
}

func helpMapString(value any, key string) string {
	switch typed := value.(type) {
	case map[string]string:
		return cleanSummaryText(typed[key])
	case map[string]any:
		text, _ := typed[key].(string)
		return cleanSummaryText(text)
	default:
		return ""
	}
}

func indentLines(value, prefix string) string {
	if value == "" {
		return ""
	}
	lines := strings.Split(value, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
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

func renderFieldBlock(fields []terminalField) string {
	if len(fields) == 0 {
		return ""
	}
	width := 0
	for _, field := range fields {
		if labelWidth := cellWidth(field.Label); labelWidth > width {
			width = labelWidth
		}
	}
	lines := make([]string, 0, len(fields))
	for _, field := range fields {
		label := cleanCell(field.Label)
		value := cleanCell(field.Value)
		padding := strings.Repeat(" ", width-cellWidth(label))
		lines = append(lines, "  "+label+":"+padding+" "+value)
	}
	return strings.Join(lines, "\n")
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
		if i < len(widths)-1 {
			for pad := widths[i] - cellWidth(cell); pad > 0; pad-- {
				b.WriteByte(' ')
			}
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

func capitalizeFirst(value string) string {
	value = cleanSummaryText(value)
	if value == "" {
		return value
	}
	runes := []rune(value)
	if runes[0] >= 'a' && runes[0] <= 'z' {
		runes[0] = runes[0] - ('a' - 'A')
	}
	return string(runes)
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
