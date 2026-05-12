package root

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chatinfra/sched/internal/cli/clitest"
	"github.com/chatinfra/sched/internal/sched"
)

func TestHelpMissingAndUnknownCommands(t *testing.T) {
	h := clitest.New(t)

	help := h.RunRaw(t, "--help").RequireSuccess(t)
	helpDoc := clitest.RequireYAMLStdout(t, help, "help.schema.yaml")
	assertCompactRootHelp(t, helpDoc)
	for _, want := range []string{"create", "put", "history", "schemas"} {
		if !strings.Contains(help.Stdout, want) {
			t.Fatalf("help output missing %q:\n%s", want, help.Stdout)
		}
	}
	for _, retired := range []string{"job put", "job get", "job list", "job delete", "job run", "job stop"} {
		if strings.Contains(help.Stdout, retired) {
			t.Fatalf("help output still lists retired command %q:\n%s", retired, help.Stdout)
		}
	}
	for _, retired := range []string{"cleanup legacy", "migration status", "migration mark-imported"} {
		if strings.Contains(help.Stdout, retired) {
			t.Fatalf("help output still lists retired command %q:\n%s", retired, help.Stdout)
		}
	}
	if help.Stderr != "" {
		t.Fatalf("help stderr = %q", help.Stderr)
	}

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

func TestCommandHelpIncludesDefaultOutputSchema(t *testing.T) {
	h := clitest.New(t)

	result := h.RunRaw(t, "help", "create").RequireSuccess(t)
	doc := clitest.RequireYAMLStdout(t, result, "command-help.schema.yaml")
	commandHelp, ok := doc.(map[string]any)
	if !ok {
		t.Fatalf("command help YAML = %#v, want object", doc)
	}
	if commandHelp["command"] != "create" || commandHelp["schema"] != "create" || commandHelp["fullSchema"] != "create-full" {
		t.Fatalf("command help schema = %#v, want create schema", commandHelp)
	}
	output, _ := commandHelp["output"].(string)
	if !strings.Contains(output, "human summary") || !strings.Contains(output, "complete structured job metadata") {
		t.Fatalf("command help output guidance = %q", output)
	}
	if !strings.Contains(result.Stdout, "--full") {
		t.Fatalf("command help missing --full enrichment flag:\n%s", result.Stdout)
	}
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
		seen[id] = true
	}
	for _, want := range []string{"help", "command-help", "schemas", "get-full", "list-full", "delete-full", "stop-full", "history-full", "systemd/reconcile-full"} {
		if !seen[want] {
			t.Fatalf("schemas discovery missing %q: %#v", want, schemas)
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
