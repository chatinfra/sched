package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/chatinfra/sched/internal/sched"
)

type options struct {
	stateRoot    string
	opencodeHome string
	systemdDir   string
	schedBin     string
	opencodeBin  string
	systemctl    bool
	dryRun       bool
}

type errorEnvelope struct {
	Error sched.CommandError `json:"error"`
}

const (
	createUsageCommand = "sched create --command <name> --agent <name> --every <duration> --workdir <dir> [--schedule-id <id>]"
	createUsage        = "usage: " + createUsageCommand
	historyUsage       = "sched history [--schedule-id <scheduleId>] [--limit N]"
	everyExpected      = "a single Go-style duration such as 1m, 60s, or 2h"
)

func Run(args []string, stdout, stderr io.Writer) error {
	return RunWithIO(args, os.Stdin, stdout, stderr)
}

func RunWithIO(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := rejectJSONFlag(args); err != nil {
		emitError(options{}, stdout, stderr, err)
		return err
	}
	if len(args) == 0 {
		return printUsage(stdout)
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		if args[0] == "help" && len(args) > 1 {
			if err := printCommandHelp(stdout, args[1:]); err != nil {
				emitError(options{}, stdout, stderr, err)
				return err
			}
			return nil
		}
		return printUsage(stdout)
	}
	opts, rest, err := parseGlobal(args)
	if err != nil {
		emitError(opts, stdout, stderr, err)
		return err
	}
	if len(rest) == 0 {
		err := errors.New("missing command")
		emitError(opts, stdout, stderr, err)
		return err
	}
	if err := run(rest, opts, stdin, stdout); err != nil {
		emitError(opts, stdout, stderr, err)
		return err
	}
	return nil
}

func rejectJSONFlag(args []string) error {
	for _, arg := range args {
		if arg == "--json" || strings.HasPrefix(arg, "--json=") {
			return sched.NewCommandError("unsupported_flag", "--json is not supported; sched emits YAML output by default",
				sched.WithArgument("--json"),
				sched.WithExpected("YAML output without an output-format flag"),
				sched.WithHint("Remove --json and parse stdout/stderr as YAML."),
			)
		}
	}
	return nil
}

func parseGlobal(args []string) (options, []string, error) {
	opts := options{systemctl: true}
	normalized, err := normalizeGlobalFlags(args)
	if err != nil {
		return opts, nil, err
	}
	fs := flag.NewFlagSet("sched", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.stateRoot, "state-root", "", "sched state root; default $OPENCODE_HOME/.config/opencode/sched/v1")
	fs.StringVar(&opts.opencodeHome, "opencode-home", "", "OpenCode user home; default OPENCODE_HOME or current user home")
	fs.StringVar(&opts.systemdDir, "systemd-dir", "", "systemd user unit directory; default $OPENCODE_HOME/.config/systemd/user")
	fs.StringVar(&opts.schedBin, "sched-bin", "", "sched binary path embedded in service units")
	fs.StringVar(&opts.opencodeBin, "opencode-bin", "", "opencode binary used for runs; default SCHED_OPENCODE_BIN or opencode")
	fs.BoolVar(&opts.systemctl, "systemctl", true, "invoke systemctl --user while reconciling")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "plan changes without writing/removing units or crontab")
	if err := fs.Parse(normalized); err != nil {
		return opts, nil, err
	}
	if opts.opencodeHome == "" {
		home, err := sched.DefaultOpenCodeHome(os.Getenv)
		if err != nil {
			return opts, nil, err
		}
		opts.opencodeHome = home
	}
	if opts.stateRoot == "" {
		opts.stateRoot = sched.DefaultStateRoot(opts.opencodeHome)
	}
	if opts.systemdDir == "" {
		opts.systemdDir = sched.DefaultSystemdUnitDir(opts.opencodeHome)
	}
	return opts, fs.Args(), nil
}

func normalizeGlobalFlags(args []string) ([]string, error) {
	boolFlags := map[string]bool{"dry-run": true, "systemctl": true}
	valueFlags := map[string]bool{"state-root": true, "opencode-home": true, "systemd-dir": true, "sched-bin": true, "opencode-bin": true}
	globals := make([]string, 0, len(args))
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if !strings.HasPrefix(arg, "--") || arg == "--" {
			rest = append(rest, arg)
			continue
		}
		nameValue := strings.TrimPrefix(arg, "--")
		name, value, hasValue := strings.Cut(nameValue, "=")
		if name == "no-systemctl" {
			globals = append(globals, "--systemctl=false")
			continue
		}
		if boolFlags[name] {
			if hasValue {
				globals = append(globals, "--"+name+"="+value)
			} else {
				globals = append(globals, "--"+name)
			}
			continue
		}
		if valueFlags[name] {
			globals = append(globals, "--"+name)
			if hasValue {
				globals = append(globals, value)
				continue
			}
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag --%s requires a value", name)
			}
			i++
			globals = append(globals, args[i])
			continue
		}
		rest = append(rest, arg)
	}
	return append(globals, rest...), nil
}

func run(args []string, opts options, stdin io.Reader, stdout io.Writer) error {
	store, err := sched.NewStore(opts.stateRoot)
	if err != nil {
		return err
	}
	cmd, cmdArgs := args[0], args[1:]
	switch cmd {
	case "create":
		return runCreateCommand(store, opts, stdout, cmdArgs)
	case "put", "get", "list", "delete", "run", "stop":
		return runScheduleCommand(store, opts, stdin, stdout, args)
	case "history":
		return runHistoryCommand(store, opts, stdout, cmdArgs)
	case "export":
		exported, err := store.Export()
		if err != nil {
			return err
		}
		return writeYAML(stdout, exported)
	case "schemas", "schema":
		return writeYAML(stdout, schemaDiscovery())
	case "systemd":
		return runSystemdCommand(store, opts, stdout, cmdArgs)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func runCreateCommand(store *sched.Store, opts options, stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("sched create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	commandName := fs.String("command", "", "OpenCode command name")
	agentName := fs.String("agent", "", "OpenCode agent name")
	every := fs.String("every", "", "interval duration such as 5s, 1m, or 2h")
	workdir := fs.String("workdir", "", "OpenCode working directory")
	scheduleID := fs.String("schedule-id", "", "safe schedule identifier")
	if err := fs.Parse(args); err != nil {
		return createFlagParseError(err)
	}
	if err := splitEveryValueError(strings.TrimSpace(*every), fs.Args()); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return createUnexpectedArgsError(fs.Args())
	}
	command := strings.TrimSpace(*commandName)
	agent := strings.TrimSpace(*agentName)
	intervalRaw := strings.TrimSpace(*every)
	dir := strings.TrimSpace(*workdir)
	if command == "" {
		return createMissingFlagError("--command")
	}
	if agent == "" {
		return createMissingFlagError("--agent")
	}
	if intervalRaw == "" {
		return createMissingFlagError("--every")
	}
	if dir == "" {
		return createMissingFlagError("--workdir")
	}
	interval, err := sched.NormalizeIntervalDuration(intervalRaw)
	if err != nil {
		return annotateEveryError(err, intervalRaw)
	}
	commandID := safeMetadataID(command, "command")
	agentID := safeMetadataID(agent, "agent")
	id := strings.TrimSpace(*scheduleID)
	if id == "" {
		id = "local-" + commandID + "-" + agentID + "-" + shortMetadataHash(command, agent, interval, dir)
	}
	if err := sched.ValidateScheduleID(id); err != nil {
		return sched.AnnotateError(err,
			sched.WithArgument("--schedule-id"),
			sched.WithReceived(id),
			sched.WithExpected("a safe identifier containing letters, numbers, dots, underscores, or hyphens"),
			sched.WithHint("Use a schedule ID without path separators or traversal segments."),
			sched.WithUsage(createUsageCommand),
		)
	}
	job := sched.Job{
		ScheduleID:       id,
		TenantID:         "local",
		OpenCodeID:       "local",
		AgentID:          agentID,
		AgentName:        agent,
		CommandID:        commandID,
		CommandName:      command,
		ScheduleKind:     sched.ScheduleKindInterval,
		IntervalDuration: interval,
		Status:           sched.StatusActive,
		Workdir:          dir,
	}
	persisted, err := store.PutJob(job, nowUTC())
	if err != nil {
		return err
	}
	if _, err := reconcile(store, opts); err != nil {
		return err
	}
	return writeYAML(stdout, persisted)
}

func createFlagParseError(err error) *sched.CommandError {
	message := err.Error()
	opts := []sched.CommandErrorOption{sched.WithUsage(createUsageCommand)}
	if argument := flagParseArgument(message); argument != "" {
		opts = append(opts, sched.WithArgument(argument))
	}
	return sched.NewCommandError("invalid_usage", message, opts...)
}

func createMissingFlagError(argument string) *sched.CommandError {
	return sched.NewCommandError("missing_required_flag", fmt.Sprintf("%s is required; %s", argument, createUsage),
		sched.WithArgument(argument),
		sched.WithUsage(createUsageCommand),
	)
}

func createUnexpectedArgsError(args []string) *sched.CommandError {
	received := strings.Join(args, " ")
	message := fmt.Sprintf("unexpected argument %q", received)
	if len(args) > 1 {
		message = fmt.Sprintf("unexpected arguments %q", received)
	}
	return sched.NewCommandError("invalid_usage", message,
		sched.WithArgument("args"),
		sched.WithReceived(received),
		sched.WithUsage(createUsageCommand),
	)
}

func flagParseArgument(message string) string {
	index := strings.LastIndex(message, "-")
	if index < 0 {
		return ""
	}
	name := strings.Trim(strings.Fields(message[index:])[0], " -:")
	if name == "" {
		return ""
	}
	return "--" + name
}

func splitEveryValueError(value string, remaining []string) error {
	if value == "" || len(remaining) == 0 || !isPositiveNumber(value) {
		return nil
	}
	unit := strings.Trim(remaining[0], "'\" ")
	if _, ok := durationUnitSuffix(unit); !ok {
		return nil
	}
	received := strings.TrimSpace(value + " " + unit)
	return sched.NewCommandError("invalid_interval", fmt.Sprintf("interval duration %q is invalid", received), everyDiagnosticOptions(received)...)
}

func annotateEveryError(err error, received string) error {
	return sched.AnnotateError(err, everyDiagnosticOptions(received)...)
}

func everyDiagnosticOptions(received string) []sched.CommandErrorOption {
	opts := []sched.CommandErrorOption{
		sched.WithArgument("--every"),
		sched.WithReceived(received),
		sched.WithExpected(everyExpected),
		sched.WithHint("Use one duration token; for one minute pass --every 1m."),
		sched.WithExamples("--every 1m", "--every 60s", "--every 2h"),
		sched.WithUsage(createUsageCommand),
	}
	if suggestion := durationSuggestion(received); suggestion != "" {
		opts = append(opts, sched.WithSuggestion(suggestion))
	}
	return opts
}

func durationSuggestion(received string) string {
	fields := strings.Fields(strings.Trim(received, "'\" "))
	if len(fields) != 2 || !isPositiveNumber(fields[0]) {
		return ""
	}
	suffix, ok := durationUnitSuffix(fields[1])
	if !ok {
		return ""
	}
	return "--every " + fields[0] + suffix
}

func durationUnitSuffix(unit string) (string, bool) {
	switch strings.Trim(strings.ToLower(unit), ".,;:") {
	case "nanosecond", "nanoseconds", "ns":
		return "ns", true
	case "microsecond", "microseconds", "us", "µs":
		return "us", true
	case "millisecond", "milliseconds", "ms":
		return "ms", true
	case "second", "seconds", "sec", "secs", "s":
		return "s", true
	case "minute", "minutes", "min", "mins", "m":
		return "m", true
	case "hour", "hours", "hr", "hrs", "h":
		return "h", true
	}
	return "", false
}

func isPositiveNumber(value string) bool {
	number, err := strconv.ParseFloat(value, 64)
	return err == nil && number > 0
}

func runScheduleCommand(store *sched.Store, opts options, stdin io.Reader, stdout io.Writer, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: sched <put|get|list|delete|run|stop> ...")
	}
	switch args[0] {
	case "put":
		fs := flag.NewFlagSet("sched put", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fromStdin := fs.Bool("stdin", false, "read job JSON from stdin")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if !*fromStdin {
			return errors.New("usage: sched put --stdin")
		}
		data, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		var job sched.Job
		if err := json.Unmarshal(data, &job); err != nil {
			return sched.WrapError("invalid_json", err, "failed to parse job JSON")
		}
		persisted, err := store.PutJob(job, nowUTC())
		if err != nil {
			return err
		}
		if _, err := reconcile(store, opts); err != nil {
			return err
		}
		return writeYAML(stdout, persisted)
	case "get":
		if len(args) != 2 {
			return errors.New("usage: sched get <scheduleId>")
		}
		job, err := store.GetJob(args[1])
		if err != nil {
			return err
		}
		return writeYAML(stdout, job)
	case "list":
		jobs, err := store.ListJobs()
		if err != nil {
			return err
		}
		return writeYAML(stdout, map[string]any{"jobs": jobs})
	case "delete":
		if len(args) != 2 {
			return errors.New("usage: sched delete <scheduleId>")
		}
		deleted, err := store.DeleteJob(args[1])
		if err != nil {
			return err
		}
		if _, err := reconcile(store, opts); err != nil {
			return err
		}
		payload := map[string]any{"scheduleId": args[1], "deleted": deleted}
		return writeYAML(stdout, payload)
	case "run":
		if len(args) < 2 {
			return errors.New("usage: sched run <scheduleId> [--source manual|scheduled]")
		}
		scheduleID := args[1]
		fs := flag.NewFlagSet("sched run", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		source := fs.String("source", sched.RunSourceManual, "run source: manual or scheduled")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if *source != sched.RunSourceManual && *source != sched.RunSourceScheduled {
			return sched.Errorf("invalid_run_source", "source must be %q or %q", sched.RunSourceManual, sched.RunSourceScheduled)
		}
		record, err := sched.RunJob(store, scheduleID, sched.RunOptions{Source: *source, OpenCodeBin: opts.opencodeBin, OpenCodeHome: opts.opencodeHome})
		if err != nil {
			return err
		}
		return writeYAML(stdout, record)
	case "stop":
		if len(args) != 2 {
			return errors.New("usage: sched stop <scheduleId>")
		}
		result, err := sched.StopSystemdJob(store, args[1], sched.SystemdOptions{
			UnitDir:      opts.systemdDir,
			StateRoot:    opts.stateRoot,
			OpenCodeHome: opts.opencodeHome,
			SchedBin:     opts.schedBin,
			Apply:        opts.systemctl,
			DryRun:       opts.dryRun,
		})
		if err != nil {
			return err
		}
		return writeYAML(stdout, result)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runHistoryCommand(store *sched.Store, opts options, stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("sched history", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	scheduleID := fs.String("schedule-id", "", "schedule ID")
	limit := fs.Int("limit", 100, "maximum records")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: %s", historyUsage)
	}
	id := strings.TrimSpace(*scheduleID)
	if id != "" {
		runs, err := store.ListRuns(id, *limit)
		if err != nil {
			return err
		}
		payload := map[string]any{"scheduleId": id, "runs": runs}
		return writeYAML(stdout, payload)
	}
	runs, err := store.ListAllRuns(*limit)
	if err != nil {
		return err
	}
	payload := map[string]any{"runs": runs}
	return writeYAML(stdout, payload)
}

func runSystemdCommand(store *sched.Store, opts options, stdout io.Writer, args []string) error {
	if len(args) != 1 || args[0] != "reconcile" {
		return errors.New("usage: sched systemd reconcile")
	}
	result, err := reconcile(store, opts)
	if err != nil {
		return err
	}
	return writeYAML(stdout, result)
}

func reconcile(store *sched.Store, opts options) (sched.ReconcileResult, error) {
	return sched.ReconcileSystemd(store, sched.SystemdOptions{
		UnitDir:      opts.systemdDir,
		StateRoot:    opts.stateRoot,
		OpenCodeHome: opts.opencodeHome,
		SchedBin:     opts.schedBin,
		Apply:        opts.systemctl,
		DryRun:       opts.dryRun,
	})
}

func emitError(opts options, stdout, stderr io.Writer, err error) {
	_ = writeYAML(stderr, errorEnvelope{Error: sched.ErrorDetails(err)})
}

func writeYAML(out io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	data, err = yaml.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	return err
}

func nowUTC() time.Time {
	return time.Now().UTC()
}

func safeMetadataID(raw, fallback string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	value := strings.Trim(b.String(), "-")
	if value == "" {
		value = fallback
	}
	if len(value) > 48 {
		value = strings.Trim(value[:48], "-")
	}
	if value == "" {
		return fallback
	}
	return value
}

func shortMetadataHash(values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:])[:8]
}

func printUsage(out io.Writer) error {
	return writeYAML(out, rootHelp())
}

func printCommandHelp(out io.Writer, args []string) error {
	command := strings.Join(args, " ")
	if command == "" {
		return printUsage(out)
	}
	for _, item := range commandHelps() {
		if item["command"] == command {
			return writeYAML(out, item)
		}
	}
	return sched.NewCommandError("unknown_command", fmt.Sprintf("unknown command %q", command))
}

func rootHelp() map[string]any {
	commands := []map[string]any{}
	for _, item := range commandHelps() {
		commands = append(commands, map[string]any{
			"name":    item["command"],
			"summary": item["summary"],
		})
	}
	return map[string]any{
		"tool":    "sched",
		"summary": "local ChatInfra command scheduler",
		"usage":   "sched [global flags] <command> [command args]",
		"flags": []map[string]any{
			{"name": "--opencode-home DIR", "summary": "OpenCode user home; default OPENCODE_HOME or user home"},
			{"name": "--state-root DIR", "summary": "state root; default $OPENCODE_HOME/.config/opencode/sched/v1"},
			{"name": "--systemd-dir DIR", "summary": "systemd user unit directory"},
			{"name": "--sched-bin PATH", "summary": "sched binary path embedded in service units"},
			{"name": "--opencode-bin PATH", "summary": "opencode binary for run supervisor"},
			{"name": "--systemctl=false", "summary": "do not invoke systemctl --user"},
			{"name": "--no-systemctl", "summary": "alias for --systemctl=false"},
			{"name": "--dry-run", "summary": "plan without writing/removing scheduler artifacts"},
		},
		"commands": commands,
		"discovery": map[string]string{
			"commandHelp": "sched help <command>",
			"schemas":     "sched schemas",
		},
	}
}

func commandHelps() []map[string]any {
	return []map[string]any{
		{"command": "create", "summary": "create a local interval schedule", "usage": createUsageCommand, "flags": []map[string]any{{"name": "--command <name>", "summary": "OpenCode command name"}, {"name": "--agent <name>", "summary": "OpenCode agent name"}, {"name": "--every <duration>", "summary": "Go duration such as 5s, 1m, or 2h"}, {"name": "--workdir <dir>", "summary": "OpenCode working directory"}, {"name": "--schedule-id <id>", "summary": "optional safe schedule identifier"}}, "schema": "create"},
		{"command": "put", "summary": "upsert a schedule job from JSON stdin", "usage": "sched put --stdin", "flags": []map[string]any{{"name": "--stdin", "summary": "read job JSON from stdin"}}, "schema": "put"},
		{"command": "get", "summary": "read one schedule job", "usage": "sched get <scheduleId>", "schema": "get"},
		{"command": "list", "summary": "list schedule jobs", "usage": "sched list", "schema": "list"},
		{"command": "delete", "summary": "delete one schedule job and reconcile units", "usage": "sched delete <scheduleId>", "schema": "delete"},
		{"command": "run", "summary": "run one schedule through the scheduler envelope", "usage": "sched run <scheduleId> [--source manual|scheduled]", "flags": []map[string]any{{"name": "--source manual|scheduled", "summary": "run source"}}, "schema": "run"},
		{"command": "stop", "summary": "stop active systemd work for a schedule", "usage": "sched stop <scheduleId>", "schema": "stop"},
		{"command": "history", "summary": "list scheduler envelope run history", "usage": historyUsage, "flags": []map[string]any{{"name": "--schedule-id <scheduleId>", "summary": "filter by schedule"}, {"name": "--limit N", "summary": "maximum records"}}, "schema": "history"},
		{"command": "export", "summary": "export schedule state", "usage": "sched export", "schema": "export"},
		{"command": "systemd reconcile", "summary": "render and reconcile user-systemd units", "usage": "sched systemd reconcile", "schema": "systemd/reconcile"},
		{"command": "schemas", "summary": "list output schemas", "usage": "sched schemas", "schema": "schemas"},
	}
}

func schemaDiscovery() map[string]any {
	return map[string]any{
		"tool": "sched",
		"schemas": []map[string]string{
			{"id": "help", "path": "spec/outputs/help.schema.yaml"},
			{"id": "command-help", "path": "spec/outputs/command-help.schema.yaml"},
			{"id": "error", "path": "spec/outputs/error.schema.yaml"},
			{"id": "create", "path": "spec/outputs/create.schema.yaml"},
			{"id": "put", "path": "spec/outputs/put.schema.yaml"},
			{"id": "get", "path": "spec/outputs/get.schema.yaml"},
			{"id": "list", "path": "spec/outputs/list.schema.yaml"},
			{"id": "delete", "path": "spec/outputs/delete.schema.yaml"},
			{"id": "run", "path": "spec/outputs/run.schema.yaml"},
			{"id": "stop", "path": "spec/outputs/stop.schema.yaml"},
			{"id": "history", "path": "spec/outputs/history.schema.yaml"},
			{"id": "export", "path": "spec/outputs/export.schema.yaml"},
			{"id": "systemd/reconcile", "path": "spec/outputs/systemd/reconcile.schema.yaml"},
			{"id": "schemas", "path": "spec/outputs/schemas.schema.yaml"},
		},
	}
}
