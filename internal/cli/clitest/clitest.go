package clitest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/chatinfra/sched/internal/cli"
	"github.com/chatinfra/sched/internal/sched"
)

type Harness struct {
	Root         string
	StateRoot    string
	OpenCodeHome string
	SystemdDir   string
	BinDir       string
}

type Result struct {
	Stdout string
	Stderr string
	Err    error
}

type ErrorEnvelope struct {
	Error sched.CommandError `json:"error"`
}

type SummaryResponse struct {
	Summary string `json:"summary"`
}

type ListResponse struct {
	Jobs string `json:"jobs"`
}

type HistoryResponse struct {
	ScheduleID string `json:"scheduleId"`
	Runs       string `json:"runs"`
}

type ReconcileSummary struct {
	DryRun   bool   `json:"dryRun"`
	Units    string `json:"units"`
	Removed  string `json:"removed"`
	Warnings string `json:"warnings"`
}

type JobOption func(*sched.Job)

func New(t *testing.T) *Harness {
	t.Helper()
	root := TempDir(t, "sched-cli-test-*")
	h := &Harness{
		Root:         root,
		StateRoot:    filepath.Join(root, "state"),
		OpenCodeHome: filepath.Join(root, "home"),
		SystemdDir:   filepath.Join(root, "systemd"),
		BinDir:       filepath.Join(root, "bin"),
	}
	for _, dir := range []string{h.OpenCodeHome, h.SystemdDir, h.BinDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", dir, err)
		}
	}
	t.Setenv("OPENCODE_HOME", h.OpenCodeHome)
	return h
}

func TempDir(t *testing.T, pattern string) string {
	t.Helper()
	base := strings.TrimSpace(os.Getenv("SUPER_TMP_DIR"))
	if base == "" {
		base = filepath.Join(moduleRoot(t), "tmp")
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", base, err)
	}
	dir, err := os.MkdirTemp(base, pattern)
	if err != nil {
		t.Fatalf("MkdirTemp(%s, %s) error = %v", base, pattern, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found from %s", dir)
		}
		dir = parent
	}
}

func (h *Harness) ValueFlags() []string {
	return []string{"--state-root", h.StateRoot, "--opencode-home", h.OpenCodeHome, "--systemd-dir", h.SystemdDir}
}

func (h *Harness) CommonFlags() []string {
	flags := append([]string{}, h.ValueFlags()...)
	return append(flags, "--no-systemctl")
}

func (h *Harness) Args(args ...string) []string {
	return append(append([]string{}, h.CommonFlags()...), args...)
}

func (h *Harness) ArgsWithFlags(flags []string, args ...string) []string {
	merged := append([]string{}, h.ValueFlags()...)
	merged = append(merged, flags...)
	return append(merged, args...)
}

func (h *Harness) Run(t *testing.T, args ...string) Result {
	t.Helper()
	return h.RunWithStdin(t, "", args...)
}

func (h *Harness) RunWithStdin(t *testing.T, stdin string, args ...string) Result {
	t.Helper()
	return RunArgsWithStdin(t, stdin, h.Args(args...))
}

func (h *Harness) RunWithFlags(t *testing.T, stdin string, flags []string, args ...string) Result {
	t.Helper()
	return RunArgsWithStdin(t, stdin, h.ArgsWithFlags(flags, args...))
}

func (h *Harness) RunRaw(t *testing.T, args ...string) Result {
	t.Helper()
	return RunArgsWithStdin(t, "", args)
}

func RunArgsWithStdin(t *testing.T, stdin string, args []string) Result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := cli.RunWithIO(args, strings.NewReader(stdin), &stdout, &stderr)
	return Result{Stdout: stdout.String(), Stderr: stderr.String(), Err: err}
}

func (r Result) RequireSuccess(t *testing.T) Result {
	t.Helper()
	if r.Err != nil {
		t.Fatalf("RunWithIO() error = %v\nstdout=%s\nstderr=%s", r.Err, r.Stdout, r.Stderr)
	}
	return r
}

func (r Result) RequireError(t *testing.T) Result {
	t.Helper()
	if r.Err == nil {
		t.Fatalf("RunWithIO() succeeded unexpectedly\nstdout=%s\nstderr=%s", r.Stdout, r.Stderr)
	}
	return r
}

func DecodeJSON[T any](t *testing.T, raw string) T {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("JSON invalid: %v\n%s", err, raw)
	}
	return value
}

func DecodeError(t *testing.T, raw string) ErrorEnvelope {
	t.Helper()
	return DecodeJSON[ErrorEnvelope](t, raw)
}

func DecodeYAML(t *testing.T, raw string) any {
	t.Helper()
	var value any
	if err := yaml.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("YAML invalid: %v\n%s", err, raw)
	}
	return normalizeYAML(value)
}

func DecodeYAMLAs[T any](t *testing.T, raw string) T {
	t.Helper()
	doc := DecodeYAML(t, raw)
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal YAML document as JSON: %v\ndoc=%#v", err, doc)
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode YAML document: %v\n%s", err, raw)
	}
	return value
}

func RequireYAMLStdout(t *testing.T, result Result, schemaPath string) any {
	t.Helper()
	if strings.TrimSpace(result.Stdout) == "" {
		t.Fatalf("stdout is empty; want YAML output")
	}
	if result.Stderr != "" {
		t.Fatalf("stderr = %q; want empty stderr for success", result.Stderr)
	}
	doc := DecodeYAML(t, result.Stdout)
	if schemaPath != "" {
		ValidateSchema(t, doc, schemaPath)
	}
	return doc
}

func RequireTextStdout(t *testing.T, result Result) string {
	t.Helper()
	if strings.TrimSpace(result.Stdout) == "" {
		t.Fatalf("stdout is empty; want terminal text output")
	}
	if result.Stderr != "" {
		t.Fatalf("stderr = %q; want empty stderr for success", result.Stderr)
	}
	return result.Stdout
}

func RequireYAMLError(t *testing.T, result Result, schemaPath string) any {
	t.Helper()
	if result.Stdout != "" {
		t.Fatalf("stdout = %q; want empty stdout for error", result.Stdout)
	}
	if strings.TrimSpace(result.Stderr) == "" {
		t.Fatalf("stderr is empty; want YAML error output")
	}
	doc := DecodeYAML(t, result.Stderr)
	if schemaPath != "" {
		ValidateSchema(t, doc, schemaPath)
	}
	return doc
}

func ValidateSchema(t *testing.T, doc any, schemaPath string) {
	t.Helper()
	schemaFile := filepath.Join(moduleRoot(t), "spec", "outputs", filepath.FromSlash(schemaPath))
	raw, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatalf("read schema %s: %v", schemaFile, err)
	}
	var schemaDoc any
	if err := yaml.Unmarshal(raw, &schemaDoc); err != nil {
		t.Fatalf("schema YAML invalid %s: %v", schemaFile, err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	url := "file://" + filepath.ToSlash(schemaFile)
	if err := compiler.AddResource(url, normalizeYAML(schemaDoc)); err != nil {
		t.Fatalf("add schema %s: %v", schemaFile, err)
	}
	schema, err := compiler.Compile(url)
	if err != nil {
		t.Fatalf("compile schema %s: %v", schemaFile, err)
	}
	if err := schema.Validate(doc); err != nil {
		t.Fatalf("YAML does not match schema %s: %v\ndoc=%#v", schemaFile, err, doc)
	}
}

func normalizeYAML(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = normalizeYAML(item)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[fmt.Sprint(key)] = normalizeYAML(item)
		}
		return out
	case []any:
		for i, item := range typed {
			typed[i] = normalizeYAML(item)
		}
		return typed
	default:
		return value
	}
}

func JobJSON(t *testing.T, job sched.Job) string {
	t.Helper()
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("Marshal(job) error = %v", err)
	}
	return string(data)
}

func SampleJob(opts ...JobOption) sched.Job {
	job := sched.Job{
		ScheduleID:         "sched-1",
		TenantID:           "tenant-1",
		OpenCodeID:         "opencode-1",
		AgentID:            "agent-1",
		AgentName:          "build-agent",
		CommandID:          "cmd-1",
		CommandName:        "Daily-Backup",
		ScheduleExpression: "0 9 * * *",
		Timezone:           "UTC",
		Status:             sched.StatusActive,
		Workdir:            "/data/opencode/work",
	}
	for _, opt := range opts {
		opt(&job)
	}
	return job
}

func SampleJobJSON(t *testing.T, opts ...JobOption) string {
	t.Helper()
	return JobJSON(t, SampleJob(opts...))
}

func WithScheduleID(id string) JobOption {
	return func(job *sched.Job) { job.ScheduleID = id }
}

func WithOpenCodeID(id string) JobOption {
	return func(job *sched.Job) { job.OpenCodeID = id }
}

func WithCommandID(id string) JobOption {
	return func(job *sched.Job) { job.CommandID = id }
}

func WithCommandName(name string) JobOption {
	return func(job *sched.Job) { job.CommandName = name }
}

func WithAgentName(name string) JobOption {
	return func(job *sched.Job) { job.AgentName = name }
}

func WithStatus(status string) JobOption {
	return func(job *sched.Job) { job.Status = status }
}

func WithTimerSuppressed(reason string) JobOption {
	return func(job *sched.Job) {
		job.TimerSuppressed = true
		job.SuppressionReason = reason
	}
}

func WithWorkdir(workdir string) JobOption {
	return func(job *sched.Job) { job.Workdir = workdir }
}

func (h *Harness) PutJob(t *testing.T, job sched.Job) sched.Job {
	t.Helper()
	result := h.RunWithStdin(t, JobJSON(t, job), "put", "--stdin", "--full").RequireSuccess(t)
	RequireYAMLStdout(t, result, "put-full.schema.yaml")
	return DecodeYAMLAs[sched.Job](t, result.Stdout)
}

func (h *Harness) JobPath(scheduleID string) string {
	return filepath.Join(h.StateRoot, "jobs", scheduleID+".json")
}

func (h *Harness) RunPath(scheduleID string) string {
	return filepath.Join(h.StateRoot, "runs", scheduleID+".jsonl")
}

func (h *Harness) LockPath(scheduleID string) string {
	return filepath.Join(h.StateRoot, "locks", scheduleID+".json")
}

func (h *Harness) FakeExecutable(t *testing.T, name string, script string) string {
	t.Helper()
	if !strings.HasPrefix(script, "#!") {
		script = "#!/bin/sh\n" + script
	}
	path := filepath.Join(h.BinDir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
	h.PrependPath(t)
	return path
}

func (h *Harness) PrependPath(t *testing.T) {
	t.Helper()
	current := os.Getenv("PATH")
	if current == "" {
		t.Setenv("PATH", h.BinDir)
		return
	}
	t.Setenv("PATH", h.BinDir+string(os.PathListSeparator)+current)
}
