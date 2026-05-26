package root

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chatinfra/sched/internal/cli/clitest"
	"github.com/chatinfra/sched/internal/sched"
)

func TestRootHelpRendersTerminalTextByDefault(t *testing.T) {
	h := clitest.New(t)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "sched"},
		{name: "sched help", args: []string{"help"}},
		{name: "sched --help", args: []string{"--help"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := h.RunRaw(t, tc.args...).RequireSuccess(t)
			text := clitest.RequireTextStdout(t, result)
			assertRootHelpText(t, text)
		})
	}
}

func TestHelpMissingAndUnknownCommands(t *testing.T) {
	h := clitest.New(t)

	missing := h.Run(t).RequireError(t)
	assertErrorEnvelope(t, missing, "internal_error")

	unknown := h.Run(t, "unknown").RequireError(t)
	assertErrorEnvelope(t, unknown, "internal_error")

	for _, args := range [][]string{{"cleanup", "legacy"}, {"migration", "status", "opencode-1"}, {"migration", "mark-imported", "opencode-1"}} {
		retired := h.Run(t, args...).RequireError(t)
		envelope := assertErrorEnvelope(t, retired, "internal_error")
		if !strings.Contains(envelope.Error.Message, `unknown command "`+args[0]+`"`) {
			t.Fatalf("retired command %v envelope=%#v", args, envelope.Error)
		}
	}
}

func TestCommandHelpRendersTerminalTextByDefault(t *testing.T) {
	h := clitest.New(t)

	for _, tc := range commandHelpTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			result := h.RunRaw(t, tc.args...).RequireSuccess(t)
			text := clitest.RequireTextStdout(t, result)
			assertCommandHelpText(t, text, tc.wantFlags)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Fatalf("command help missing %q:\n%s", want, text)
				}
			}
		})
	}
}

func TestCommandHelpAliasesMatchHelpCommand(t *testing.T) {
	h := clitest.New(t)

	for _, tc := range []struct {
		name      string
		canonical []string
		aliases   [][]string
	}{
		{name: "create", canonical: []string{"help", "create"}, aliases: [][]string{{"create", "--help"}, {"create", "-h"}}},
		{name: "systemd reconcile", canonical: []string{"help", "systemd", "reconcile"}, aliases: [][]string{{"systemd", "reconcile", "--help"}, {"systemd", "reconcile", "-h"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canonical := h.RunRaw(t, tc.canonical...).RequireSuccess(t)
			for _, alias := range tc.aliases {
				result := h.RunRaw(t, alias...).RequireSuccess(t)
				if result.Stdout != canonical.Stdout {
					t.Fatalf("alias %v stdout differs from canonical %v:\nalias:\n%s\ncanonical:\n%s", alias, tc.canonical, result.Stdout, canonical.Stdout)
				}
			}
			if _, err := os.Stat(h.StateRoot); !os.IsNotExist(err) {
				t.Fatalf("help aliases created state root %s: %v", h.StateRoot, err)
			}
		})
	}
}

func TestFullHelpEmitsStructuredYAML(t *testing.T) {
	h := clitest.New(t)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "sched help --full", args: []string{"help", "--full"}},
		{name: "sched --help --full", args: []string{"--help", "--full"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := h.RunRaw(t, tc.args...).RequireSuccess(t)
			helpDoc := clitest.RequireYAMLStdout(t, result, "help.schema.yaml")
			assertCompactRootHelp(t, helpDoc)
		})
	}

	result := h.RunRaw(t, "help", "create", "--full").RequireSuccess(t)
	doc := clitest.RequireYAMLStdout(t, result, "command-help.schema.yaml")
	assertCreateCommandHelpYAML(t, doc, result.Stdout)
}

func TestCommandHelpDescribesTerminalDefaultsAndStructuredSchemas(t *testing.T) {
	h := clitest.New(t)

	result := h.RunRaw(t, "help", "create", "--full").RequireSuccess(t)
	doc := clitest.RequireYAMLStdout(t, result, "command-help.schema.yaml")
	assertCreateCommandHelpYAML(t, doc, result.Stdout)
}

func assertRootHelpText(t *testing.T, text string) {
	t.Helper()
	assertTerminalText(t, text)
	assertHelpSections(t, text, []string{"USAGE", "COMMANDS", "FLAGS", "OUTPUT", "EXAMPLES", "SEE ALSO"})
	for _, want := range []string{
		"sched - local ChatInfra command scheduler",
		"sched [global flags] <command> [command args]",
		"create", "put", "history", "schemas",
		"--opencode-home DIR", "default: OPENCODE_HOME or current user home",
		"--opencode-bin PATH", "default: SCHED_OPENCODE_BIN or opencode",
		"--systemctl BOOL", "default: true",
		"--dry-run", "default: false",
		"stdout: default operator commands emit concise terminal text",
		"stderr: errors emit the YAML error envelope",
		"sched create --command hello --agent syslog --every 5s --workdir /data/opencode/work",
		"sched help --full", "sched help <command>", "sched schemas",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("root help missing %q:\n%s", want, text)
		}
	}
	for _, retired := range []string{"job put", "job get", "job list", "job delete", "job run", "job stop"} {
		if strings.Contains(text, retired) {
			t.Fatalf("help output still lists retired command %q:\n%s", retired, text)
		}
	}
	for _, retired := range []string{"cleanup legacy", "migration status", "migration mark-imported"} {
		if strings.Contains(text, retired) {
			t.Fatalf("help output still lists retired command %q:\n%s", retired, text)
		}
	}
}

type commandHelpCase struct {
	name      string
	args      []string
	wantFlags bool
	want      []string
}

func commandHelpTestCases() []commandHelpCase {
	return []commandHelpCase{
		{
			name:      "create",
			args:      []string{"help", "create"},
			wantFlags: true,
			want:      []string{"sched create - create a local interval schedule", createUsageFragment(), "--command <name>", "--schedule-id <id>", "default: derived", "--full", "default: false", "Default stdout is terminal text", "Full schema: create-full", "sched help create --full", "sched schemas", "sched create --command hello --agent syslog --every 5s --workdir /data/opencode/work"},
		},
		{
			name:      "put",
			args:      []string{"help", "put"},
			wantFlags: true,
			want:      []string{"sched put - upsert a schedule job from JSON stdin", "sched put --stdin [--full]", "--stdin", "default: false", "Full schema: put-full", "sched put --stdin --full < job.json"},
		},
		{
			name:      "get",
			args:      []string{"help", "get"},
			wantFlags: true,
			want:      []string{"sched get - read one schedule job", "sched get <scheduleId> [--full]", "Full schema: get-full", "sched get sched-1 --full"},
		},
		{
			name:      "list",
			args:      []string{"help", "list"},
			wantFlags: true,
			want:      []string{"sched list - list schedule jobs", "sched list [--full]", "terminal table", "Full schema: list-full", "sched list --full"},
		},
		{
			name:      "delete",
			args:      []string{"help", "delete"},
			wantFlags: true,
			want:      []string{"sched delete - delete one schedule job and reconcile units", "sched delete <scheduleId> [--full]", "Full schema: delete-full", "sched delete sched-1 --full"},
		},
		{
			name:      "run",
			args:      []string{"help", "run"},
			wantFlags: true,
			want:      []string{"sched run - run one schedule through the scheduler envelope", "sched run <scheduleId> [--source manual|scheduled] [--full]", "--source manual|scheduled", "default: manual", "Full schema: run-full", "sched run sched-1 --source manual"},
		},
		{
			name:      "stop",
			args:      []string{"help", "stop"},
			wantFlags: true,
			want:      []string{"sched stop - stop active systemd work for a schedule", "sched stop <scheduleId> [--full]", "Full schema: stop-full", "sched stop sched-1 --full"},
		},
		{
			name:      "history",
			args:      []string{"help", "history"},
			wantFlags: true,
			want:      []string{"sched history - list scheduler envelope run history", "sched history [--schedule-id <scheduleId>] [--limit N] [--full]", "--limit N", "default: 100", "Full schema: history-full", "sched history --schedule-id sched-1 --limit 20"},
		},
		{
			name:      "export",
			args:      []string{"help", "export"},
			wantFlags: false,
			want:      []string{"sched export - export schedule state", "sched export", "Schema: export", "sched export > sched-state.yaml"},
		},
		{
			name:      "systemd reconcile",
			args:      []string{"help", "systemd", "reconcile"},
			wantFlags: true,
			want:      []string{"sched systemd reconcile - render and reconcile user-systemd units", "sched systemd reconcile [--full]", "--full", "default: false", "terminal sections", "Full schema: systemd/reconcile-full", "sched help systemd reconcile --full", "sched systemd reconcile --full"},
		},
		{
			name:      "schemas",
			args:      []string{"help", "schemas"},
			wantFlags: false,
			want:      []string{"sched schemas - list output schemas", "sched schemas", "Schema: schemas", "structured YAML schema discovery"},
		},
	}
}

func assertCommandHelpText(t *testing.T, text string, wantFlags bool) {
	t.Helper()
	assertTerminalText(t, text)
	sections := []string{"USAGE", "OUTPUT", "EXAMPLES", "SEE ALSO"}
	if wantFlags {
		sections = []string{"USAGE", "FLAGS", "OUTPUT", "EXAMPLES", "SEE ALSO"}
	}
	assertHelpSections(t, text, sections)
	for _, want := range []string{"stderr: errors emit the YAML error envelope", "sched schemas"} {
		if !strings.Contains(text, want) {
			t.Fatalf("command help missing %q:\n%s", want, text)
		}
	}
}

func assertHelpSections(t *testing.T, text string, sections []string) {
	t.Helper()
	last := -1
	for _, section := range sections {
		marker := "\n\n" + section + "\n"
		index := strings.Index(text, marker)
		if index < 0 {
			t.Fatalf("help missing section %q:\n%s", section, text)
		}
		if index <= last {
			t.Fatalf("help section %q appears out of order:\n%s", section, text)
		}
		last = index
	}
}

func assertCreateCommandHelpYAML(t *testing.T, doc any, stdout string) {
	t.Helper()
	commandHelp, ok := doc.(map[string]any)
	if !ok {
		t.Fatalf("command help YAML = %#v, want object", doc)
	}
	if commandHelp["command"] != "create" || commandHelp["fullSchema"] != "create-full" {
		t.Fatalf("command help schema = %#v, want create full schema", commandHelp)
	}
	if _, ok := commandHelp["schema"]; ok {
		t.Fatalf("terminal-first command help advertises default schema: %#v", commandHelp)
	}
	output, _ := commandHelp["output"].(string)
	if !strings.Contains(output, "terminal text") || !strings.Contains(output, "structured YAML job metadata") {
		t.Fatalf("command help output guidance = %q", output)
	}
	if !strings.Contains(stdout, "--full") {
		t.Fatalf("command help missing --full enrichment flag:\n%s", stdout)
	}
}

func assertTerminalText(t *testing.T, text string) {
	t.Helper()
	for _, wrapper := range []string{"tool:", "command:", "summary:", "usage:"} {
		if strings.Contains(text, wrapper) {
			t.Fatalf("terminal help contains YAML object wrapper %q:\n%s", wrapper, text)
		}
	}
}

func createUsageFragment() string {
	return "sched create --command <name> --agent <name> --every <duration> --workdir <dir> [--schedule-id <id>] [--full]"
}

func TestSchemasDiscoveryOutput(t *testing.T) {
	h := clitest.New(t)

	result := h.Run(t, "schemas").RequireSuccess(t)
	doc := clitest.RequireYAMLStdout(t, result, "schemas.schema.yaml")
	discovery, ok := doc.(map[string]any)
	if !ok {
		t.Fatalf("schemas YAML = %#v, want object", doc)
	}
	schemas, ok := discovery["schemas"].([]any)
	if !ok || len(schemas) == 0 {
		t.Fatalf("schemas discovery missing schemas: %#v", discovery)
	}
	seen := map[string]bool{}
	for _, item := range schemas {
		schema, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("schema discovery entry = %#v, want object", item)
		}
		id, _ := schema["id"].(string)
		description, _ := schema["description"].(string)
		if description == "" {
			t.Fatalf("schema discovery entry missing description: %#v", schema)
		}
		if schema["format"] != "yaml" {
			t.Fatalf("schema discovery entry missing yaml format: %#v", schema)
		}
		seen[id] = true
	}
	for _, want := range []string{"help", "command-help", "schemas", "get-full", "list-full", "delete-full", "stop-full", "history-full", "systemd/reconcile-full"} {
		if !seen[want] {
			t.Fatalf("schemas discovery missing %q: %#v", want, schemas)
		}
	}
	for _, retiredDefault := range []string{"create", "put", "get", "list", "delete", "run", "stop", "history", "systemd/reconcile"} {
		if seen[retiredDefault] {
			t.Fatalf("schemas discovery still advertises terminal default schema %q: %#v", retiredDefault, schemas)
		}
	}
}

func TestJSONFlagRejectedWithYAMLError(t *testing.T) {
	h := clitest.New(t)
	result := h.RunRaw(t, "--json").RequireError(t)
	doc := clitest.RequireYAMLError(t, result, "error.schema.yaml")
	envelope, ok := doc.(map[string]any)["error"].(map[string]any)
	if !ok {
		t.Fatalf("error envelope missing: %#v", doc)
	}
	code, _ := envelope["code"].(string)
	message, _ := envelope["message"].(string)
	if code == "" || !strings.Contains(message, "--json") {
		t.Fatalf("unsupported --json envelope = %#v", envelope)
	}
}

func TestYAMLErrorEnvelopesAndDiagnostics(t *testing.T) {
	h := clitest.New(t)

	missing := h.Run(t, "get", "missing").RequireError(t)
	assertErrorEnvelope(t, missing, "job_not_found")

	diagnostic := h.Run(t, "create", "--command", "hello", "--agent", "syslog", "--every", "1 minute", "--workdir", filepath.Join(h.Root, "work")).RequireError(t)
	diagnosticEnvelope := assertErrorEnvelope(t, diagnostic, "invalid_interval")
	if diagnosticEnvelope.Error.Argument != "--every" || diagnosticEnvelope.Error.Message == "" || diagnosticEnvelope.Error.Received != "1 minute" || diagnosticEnvelope.Error.Expected == "" || !strings.Contains(strings.Join(diagnosticEnvelope.Error.Examples, " "), "--every 1m") {
		t.Fatalf("diagnostic YAML error = %#v", diagnosticEnvelope.Error)
	}

	for _, args := range [][]string{{"cleanup", "legacy"}, {"migration", "status", "opencode-1"}, {"migration", "mark-imported", "opencode-1"}} {
		retired := h.Run(t, args...).RequireError(t)
		envelope := assertErrorEnvelope(t, retired, "internal_error")
		if !strings.Contains(envelope.Error.Message, `unknown command "`+args[0]+`"`) {
			t.Fatalf("retired command %v YAML stderr=%q", args, retired.Stderr)
		}
	}
	jobCommand := h.Run(t, "job", "list").RequireError(t)
	envelope := assertErrorEnvelope(t, jobCommand, "internal_error")
	if !strings.Contains(envelope.Error.Message, `unknown command "job"`) {
		t.Fatalf("retired job command YAML stderr=%q", jobCommand.Stderr)
	}
}

func TestGlobalFlagNormalizationForSystemctlAliases(t *testing.T) {
	h := clitest.New(t)
	jobJSON := clitest.SampleJobJSON(t)

	noSystemctl := h.RunWithFlags(t, jobJSON, []string{"--no-systemctl"}, "put", "--stdin", "--full").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, noSystemctl, "put-full.schema.yaml")
	put := clitest.DecodeYAMLAs[sched.Job](t, noSystemctl.Stdout)
	if put.ScheduleID != "sched-1" {
		t.Fatalf("put scheduleID = %q", put.ScheduleID)
	}

	falseSystemctl := h.RunWithFlags(t, jobJSON, []string{"--systemctl=false"}, "put", "--stdin", "--full").RequireSuccess(t)
	clitest.RequireYAMLStdout(t, falseSystemctl, "put-full.schema.yaml")
	updated := clitest.DecodeYAMLAs[sched.Job](t, falseSystemctl.Stdout)
	if updated.ScheduleID != "sched-1" {
		t.Fatalf("updated scheduleID = %q", updated.ScheduleID)
	}

	entries, err := os.ReadDir(h.SystemdDir)
	if err != nil {
		t.Fatalf("ReadDir(systemd) error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected rendered service and timer, got %d entries", len(entries))
	}
}

func TestGlobalValueFlagsAndMissingValues(t *testing.T) {
	h := clitest.New(t)
	customState := filepath.Join(h.Root, "custom-state")
	customHome := filepath.Join(h.Root, "custom-home")
	customSystemd := filepath.Join(h.Root, "custom-systemd")

	result := clitest.RunArgsWithStdin(t, clitest.SampleJobJSON(t), []string{
		"put", "--stdin", "--full",
		"--state-root", customState,
		"--opencode-home", customHome,
		"--systemd-dir", customSystemd,
		"--no-systemctl",
	}).RequireSuccess(t)
	clitest.RequireYAMLStdout(t, result, "put-full.schema.yaml")
	job := clitest.DecodeYAMLAs[sched.Job](t, result.Stdout)
	if job.ScheduleID != "sched-1" {
		t.Fatalf("job scheduleID = %q", job.ScheduleID)
	}
	if _, err := os.Stat(filepath.Join(customState, "jobs", "sched-1.json")); err != nil {
		t.Fatalf("custom state job missing: %v", err)
	}

	missing := clitest.RunArgsWithStdin(t, "", []string{"--state-root"}).RequireError(t)
	envelope := assertErrorEnvelope(t, missing, "internal_error")
	if !strings.Contains(envelope.Error.Message, "flag --state-root requires a value") {
		t.Fatalf("missing global value envelope=%#v", envelope.Error)
	}
}

func assertErrorEnvelope(t *testing.T, result clitest.Result, code string) clitest.ErrorEnvelope {
	t.Helper()
	clitest.RequireYAMLError(t, result, "error.schema.yaml")
	envelope := clitest.DecodeYAMLAs[clitest.ErrorEnvelope](t, result.Stderr)
	if envelope.Error.Code != code || envelope.Error.Message == "" {
		t.Fatalf("error envelope = %#v, want code %q", envelope.Error, code)
	}
	return envelope
}

func assertCompactRootHelp(t *testing.T, doc any) {
	t.Helper()
	help, ok := doc.(map[string]any)
	if !ok {
		t.Fatalf("help YAML = %#v, want object", doc)
	}
	if _, ok := help["schemas"]; ok {
		t.Fatalf("root help includes top-level schemas inventory: %#v", help["schemas"])
	}
	discovery, ok := help["discovery"].(map[string]any)
	if !ok {
		t.Fatalf("root help discovery = %#v, want object", help["discovery"])
	}
	if discovery["schemas"] != "sched schemas" {
		t.Fatalf("root help discovery.schemas = %#v, want sched schemas", discovery["schemas"])
	}
	if discovery["commandHelp"] != "sched help <command>" {
		t.Fatalf("root help discovery.commandHelp = %#v, want sched help <command>", discovery["commandHelp"])
	}

	commands, ok := help["commands"].([]any)
	if !ok || len(commands) == 0 {
		t.Fatalf("root help commands = %#v, want non-empty array", help["commands"])
	}
	wanted := map[string]bool{"create": false, "put": false, "history": false, "schemas": false}
	for _, item := range commands {
		command, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("root help command entry = %#v, want object", item)
		}
		if _, ok := command["schema"]; ok {
			t.Fatalf("root help command entry includes schema: %#v", command)
		}
		name, _ := command["name"].(string)
		summary, _ := command["summary"].(string)
		if name == "" || summary == "" {
			t.Fatalf("root help command entry missing name or summary: %#v", command)
		}
		if _, ok := wanted[name]; ok {
			wanted[name] = true
		}
	}
	for name, found := range wanted {
		if !found {
			t.Fatalf("root help missing command %q: %#v", name, commands)
		}
	}
}
