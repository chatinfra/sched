package sched

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type RunOptions struct {
	Source       string
	OpenCodeBin  string
	OpenCodeHome string
	Now          func() time.Time
}

type lockRecord struct {
	ScheduleID string    `json:"scheduleId"`
	PID        int       `json:"pid"`
	StartedAt  time.Time `json:"startedAt"`
}

func RunJob(store *Store, scheduleID string, opts RunOptions) (RunRecord, error) {
	if store == nil {
		return RunRecord{}, Errorf("invalid_state_root", "store is required")
	}
	if err := store.Ensure(); err != nil {
		return RunRecord{}, err
	}
	job, err := store.GetJob(scheduleID)
	if err != nil {
		return RunRecord{}, err
	}
	opts = normalizeRunOptions(opts)
	startedAt := opts.Now().UTC()
	runID := newRunID(startedAt)
	logPath := filepath.Join(opts.OpenCodeHome, "log", fmt.Sprintf("sched-%s-%s.log", token(job.ScheduleID, "schedule"), runID))
	commandLine := []string{opts.OpenCodeBin, "run", "--dir", job.Workdir, "--command", job.CommandName, "--agent", job.AgentName, "--title", job.Title}
	record := RunRecord{RunID: runID, ScheduleID: job.ScheduleID, Source: opts.Source, Status: RunStatusFailed, StartedAt: startedAt, LogPath: logPath, CommandLine: commandLine}
	if job.TimerSuppressed {
		reason := strings.TrimSpace(job.SuppressionReason)
		if reason == "" {
			reason = "schedule is suppressed"
		}
		record.Status = RunStatusSkipped
		record.Message = reason
		finishedAt := opts.Now().UTC()
		record.FinishedAt = &finishedAt
		record.DurationMs = finishedAt.Sub(startedAt).Milliseconds()
		_ = store.AppendRun(record)
		return record, Errorf("job_suppressed", "schedule %s is suppressed: %s", job.ScheduleID, reason)
	}

	release, held, err := acquireLock(store, job.ScheduleID, startedAt)
	if err != nil {
		return RunRecord{}, err
	}
	if held != nil {
		record.Status = RunStatusSkipped
		record.Message = fmt.Sprintf("schedule already running with pid %d", held.PID)
		finishedAt := opts.Now().UTC()
		record.FinishedAt = &finishedAt
		record.DurationMs = finishedAt.Sub(startedAt).Milliseconds()
		_ = store.AppendRun(record)
		return record, nil
	}
	defer release()

	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return record, WrapError("run_failed", err, "failed to create log directory")
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return record, WrapError("run_failed", err, "failed to open run log")
	}
	defer logFile.Close()

	cmd := exec.Command(opts.OpenCodeBin, "run", "--dir", job.Workdir, "--command", job.CommandName, "--agent", job.AgentName, "--title", job.Title)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = runEnvironment(opts.OpenCodeHome, job.NotifyChannel, job.NotifyTo)
	runErr := cmd.Run()
	finishedAt := opts.Now().UTC()
	record.FinishedAt = &finishedAt
	record.DurationMs = finishedAt.Sub(startedAt).Milliseconds()
	if runErr == nil {
		exitCode := 0
		record.ExitCode = &exitCode
		record.Status = RunStatusSuccess
	} else {
		record.Status = RunStatusFailed
		record.Message = runErr.Error()
		exitCode := 1
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		record.ExitCode = &exitCode
	}
	appendErr := store.AppendRun(record)
	if appendErr == nil {
		appendErr = store.UpdateJobRunMetadata(job.ScheduleID, finishedAt, record.Status)
	}
	if appendErr != nil {
		return record, appendErr
	}
	if runErr != nil {
		return record, WrapError("run_failed", runErr, "opencode command failed for schedule %s", job.ScheduleID)
	}
	return record, nil
}

func normalizeRunOptions(opts RunOptions) RunOptions {
	if opts.Source == "" {
		opts.Source = RunSourceManual
	}
	if opts.Source != RunSourceManual && opts.Source != RunSourceScheduled {
		opts.Source = RunSourceManual
	}
	if opts.OpenCodeBin == "" {
		opts.OpenCodeBin = strings.TrimSpace(os.Getenv("SCHED_OPENCODE_BIN"))
	}
	if opts.OpenCodeBin == "" {
		opts.OpenCodeBin = "opencode"
	}
	if opts.OpenCodeHome == "" {
		if home, err := DefaultOpenCodeHome(os.Getenv); err == nil {
			opts.OpenCodeHome = home
		}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return opts
}

func acquireLock(store *Store, scheduleID string, startedAt time.Time) (func(), *lockRecord, error) {
	path := store.lockPath(scheduleID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, WrapError("lock_failed", err, "failed to create lock directory")
	}
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			record := lockRecord{ScheduleID: scheduleID, PID: os.Getpid(), StartedAt: startedAt}
			if encErr := json.NewEncoder(file).Encode(record); encErr != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return nil, nil, WrapError("lock_failed", encErr, "failed to write lock")
			}
			if closeErr := file.Close(); closeErr != nil {
				_ = os.Remove(path)
				return nil, nil, WrapError("lock_failed", closeErr, "failed to close lock")
			}
			return func() { _ = os.Remove(path) }, nil, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, nil, WrapError("lock_failed", err, "failed to acquire lock")
		}
		held, readErr := readLock(path)
		if readErr != nil || !processAlive(held.PID) {
			_ = os.Remove(path)
			continue
		}
		return func() {}, &held, nil
	}
	return nil, nil, Errorf("lock_failed", "failed to acquire lock for schedule %s", scheduleID)
}

func readLock(path string) (lockRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return lockRecord{}, err
	}
	var record lockRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return lockRecord{}, err
	}
	return record, nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

func runEnvironment(opencodeHome string, notifyChannel *string, notifyTo *string) []string {
	envMap := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		envMap[key] = value
	}
	if opencodeHome != "" {
		envMap["OPENCODE_LOG_DIR"] = filepath.Join(opencodeHome, "log")
	}
	if notifyChannel != nil && notifyTo != nil {
		envMap["SCHED_NOTIFY_CHANNEL"] = *notifyChannel
		envMap["SCHED_NOTIFY_TO"] = *notifyTo
	}
	envMap["OPENCODE_PERMISSION"] = mergeQuestionDeny(envMap["OPENCODE_PERMISSION"])
	keys := make([]string, 0, len(envMap))
	for key := range envMap {
		keys = append(keys, key)
	}
	sortStrings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+envMap[key])
	}
	return env
}

func mergeQuestionDeny(raw string) string {
	policy := map[string]any{"question": "deny"}
	if strings.TrimSpace(raw) != "" {
		var existing map[string]any
		if err := json.Unmarshal([]byte(raw), &existing); err == nil {
			for key, value := range existing {
				policy[key] = value
			}
			policy["question"] = "deny"
		}
	}
	data, _ := json.Marshal(policy)
	return string(data)
}

func newRunID(t time.Time) string {
	return t.UTC().Format("20060102T150405.000000000Z") + "-" + strconv.Itoa(os.Getpid())
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
