package sched

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var scheduleIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)

type Store struct {
	Root string
}

func NewStore(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, Errorf("invalid_state_root", "state root is required")
	}
	return &Store{Root: root}, nil
}

func DefaultOpenCodeHome(getenv func(string) string) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if home := strings.TrimSpace(getenv("OPENCODE_HOME")); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", WrapError("home_unavailable", err, "OPENCODE_HOME is not set and user home is unavailable")
	}
	return home, nil
}

func DefaultStateRoot(opencodeHome string) string {
	return filepath.Join(opencodeHome, ".config", "opencode", "sched", "v1")
}

func DefaultSystemdUnitDir(opencodeHome string) string {
	return filepath.Join(opencodeHome, ".config", "systemd", "user")
}

func (s *Store) Ensure() error {
	for _, dir := range []string{s.Root, s.jobsDir(), s.runsDir(), s.locksDir(), s.logsDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return WrapError("state_write_failed", err, "failed to create state directory %s", dir)
		}
	}
	return nil
}

func (s *Store) PutJob(job Job, now time.Time) (Job, error) {
	if err := s.Ensure(); err != nil {
		return Job{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	job = normalizeJob(job, now)
	if existing, err := s.GetJob(job.ScheduleID); err == nil {
		job.CreatedAt = existing.CreatedAt
		job.LastRunAt = existing.LastRunAt
		job.LastRunStatus = existing.LastRunStatus
	} else if !isNotFound(err) {
		return Job{}, err
	}
	job.UpdatedAt = now
	if err := ValidateJob(job); err != nil {
		return Job{}, err
	}
	if err := writeJSONAtomic(s.jobPath(job.ScheduleID), job, 0o644); err != nil {
		return Job{}, WrapError("state_write_failed", err, "failed to write job %s", job.ScheduleID)
	}
	return job, nil
}

func (s *Store) GetJob(scheduleID string) (Job, error) {
	if err := ValidateScheduleID(scheduleID); err != nil {
		return Job{}, err
	}
	data, err := os.ReadFile(s.jobPath(scheduleID))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Job{}, Errorf("job_not_found", "job %s not found", scheduleID)
		}
		return Job{}, WrapError("state_read_failed", err, "failed to read job %s", scheduleID)
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return Job{}, WrapError("invalid_job_state", err, "failed to parse job %s", scheduleID)
	}
	if job.ScheduleID == "" {
		job.ScheduleID = scheduleID
	}
	job = normalizeJob(job, time.Now().UTC())
	if err := ValidateJob(job); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *Store) ListJobs() ([]Job, error) {
	if err := s.Ensure(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.jobsDir())
	if err != nil {
		return nil, WrapError("state_read_failed", err, "failed to list jobs")
	}
	jobs := make([]Job, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		job, err := s.GetJob(id)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].UpdatedAt.Equal(jobs[j].UpdatedAt) {
			return jobs[i].ScheduleID < jobs[j].ScheduleID
		}
		return jobs[i].UpdatedAt.Before(jobs[j].UpdatedAt)
	})
	return jobs, nil
}

func (s *Store) DeleteJob(scheduleID string) (bool, error) {
	if err := ValidateScheduleID(scheduleID); err != nil {
		return false, err
	}
	path := s.jobPath(scheduleID)
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, WrapError("state_write_failed", err, "failed to delete job %s", scheduleID)
	}
	return true, nil
}

func (s *Store) Export() (Export, error) {
	jobs, err := s.ListJobs()
	if err != nil {
		return Export{}, err
	}
	return Export{SchemaVersion: SchemaVersion, Jobs: jobs}, nil
}

func (s *Store) AppendRun(record RunRecord) error {
	if err := s.Ensure(); err != nil {
		return err
	}
	if err := ValidateScheduleID(record.ScheduleID); err != nil {
		return err
	}
	path := s.runPath(record.ScheduleID)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return WrapError("history_write_failed", err, "failed to append run history for %s", record.ScheduleID)
	}
	defer file.Close()
	enc := json.NewEncoder(file)
	if err := enc.Encode(record); err != nil {
		return WrapError("history_write_failed", err, "failed to encode run history for %s", record.ScheduleID)
	}
	return nil
}

func (s *Store) ListRuns(scheduleID string, limit int) ([]RunRecord, error) {
	if err := ValidateScheduleID(scheduleID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	runs, err := s.readRuns(scheduleID)
	if err != nil {
		return nil, err
	}
	reverseRuns(runs)
	return limitRuns(runs, limit), nil
}

func (s *Store) ListAllRuns(limit int) ([]RunRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	if err := s.Ensure(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.runsDir())
	if err != nil {
		return nil, WrapError("history_read_failed", err, "failed to list run history")
	}
	runs := []RunRecord{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		scheduleID := strings.TrimSuffix(entry.Name(), ".jsonl")
		if err := ValidateScheduleID(scheduleID); err != nil {
			return nil, err
		}
		scheduleRuns, err := s.readRuns(scheduleID)
		if err != nil {
			return nil, err
		}
		runs = append(runs, scheduleRuns...)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].StartedAt.Equal(runs[j].StartedAt) {
			if runs[i].ScheduleID == runs[j].ScheduleID {
				return runs[i].RunID > runs[j].RunID
			}
			return runs[i].ScheduleID < runs[j].ScheduleID
		}
		return runs[i].StartedAt.After(runs[j].StartedAt)
	})
	return limitRuns(runs, limit), nil
}

func (s *Store) readRuns(scheduleID string) ([]RunRecord, error) {
	data, err := os.ReadFile(s.runPath(scheduleID))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []RunRecord{}, nil
		}
		return nil, WrapError("history_read_failed", err, "failed to read run history for %s", scheduleID)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	runs := make([]RunRecord, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record RunRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, WrapError("history_read_failed", err, "failed to parse run history for %s", scheduleID)
		}
		runs = append(runs, record)
	}
	return runs, nil
}

func reverseRuns(runs []RunRecord) {
	for i, j := 0, len(runs)-1; i < j; i, j = i+1, j-1 {
		runs[i], runs[j] = runs[j], runs[i]
	}
}

func limitRuns(runs []RunRecord, limit int) []RunRecord {
	if len(runs) > limit {
		return runs[:limit]
	}
	return runs
}

func (s *Store) UpdateJobRunMetadata(scheduleID string, finishedAt time.Time, status string) error {
	job, err := s.GetJob(scheduleID)
	if err != nil {
		return err
	}
	finishedAt = finishedAt.UTC()
	job.LastRunAt = &finishedAt
	job.LastRunStatus = status
	job.UpdatedAt = finishedAt
	if err := writeJSONAtomic(s.jobPath(job.ScheduleID), job, 0o644); err != nil {
		return WrapError("state_write_failed", err, "failed to update job %s run metadata", job.ScheduleID)
	}
	return nil
}

func ValidateScheduleID(scheduleID string) error {
	scheduleID = strings.TrimSpace(scheduleID)
	if scheduleID == "" {
		return Errorf("invalid_schedule_id", "scheduleId is required")
	}
	if strings.Contains(scheduleID, "/") || strings.Contains(scheduleID, `\`) || strings.Contains(scheduleID, "..") || !scheduleIDPattern.MatchString(scheduleID) {
		return Errorf("invalid_schedule_id", "scheduleId %q is not a safe identifier", scheduleID)
	}
	return nil
}

func NormalizeIntervalDuration(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", Errorf("invalid_interval", "interval duration is required")
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return "", WrapError("invalid_interval", err, "interval duration %q is invalid", raw)
	}
	if duration <= 0 {
		return "", Errorf("invalid_interval", "interval duration must be positive")
	}
	return duration.String(), nil
}

func ValidateIntervalDuration(raw string) error {
	_, err := NormalizeIntervalDuration(raw)
	return err
}

func ValidateJob(job Job) error {
	if err := ValidateScheduleID(job.ScheduleID); err != nil {
		return err
	}
	required := map[string]string{
		"tenantId":    job.TenantID,
		"opencodeId":  job.OpenCodeID,
		"agentId":     job.AgentID,
		"agentName":   job.AgentName,
		"commandId":   job.CommandID,
		"commandName": job.CommandName,
		"workdir":     job.Workdir,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			return Errorf("invalid_job", "%s is required", field)
		}
	}
	if job.Status != StatusActive && job.Status != StatusPaused {
		return Errorf("invalid_job", "status must be %q or %q", StatusActive, StatusPaused)
	}
	switch normalizeScheduleKind(job.ScheduleKind) {
	case ScheduleKindCron:
		if strings.TrimSpace(job.ScheduleExpression) == "" {
			return Errorf("invalid_job", "scheduleExpression is required")
		}
		if strings.TrimSpace(job.Timezone) == "" {
			return Errorf("invalid_job", "timezone is required")
		}
		if _, err := time.LoadLocation(job.Timezone); err != nil {
			return WrapError("invalid_timezone", err, "timezone %q is invalid", job.Timezone)
		}
		if _, err := CronToSystemdCalendars(job.ScheduleExpression, job.Timezone); err != nil {
			return err
		}
	case ScheduleKindInterval:
		if err := ValidateIntervalDuration(job.IntervalDuration); err != nil {
			return err
		}
	default:
		return Errorf("invalid_schedule_kind", "scheduleKind must be %q or %q", ScheduleKindCron, ScheduleKindInterval)
	}
	if expected := ExpectedTitle(job.CommandID, job.ScheduleID); job.Title != expected {
		return Errorf("invalid_job", "title must be %q", expected)
	}
	return nil
}

func normalizeJob(job Job, now time.Time) Job {
	job.SchemaVersion = SchemaVersion
	job.ScheduleID = strings.TrimSpace(job.ScheduleID)
	job.TenantID = strings.TrimSpace(job.TenantID)
	job.OpenCodeID = strings.TrimSpace(job.OpenCodeID)
	job.AgentID = strings.TrimSpace(job.AgentID)
	job.AgentName = strings.TrimSpace(job.AgentName)
	job.CommandID = strings.TrimSpace(job.CommandID)
	job.CommandName = strings.TrimSpace(job.CommandName)
	job.ScheduleKind = normalizeScheduleKind(job.ScheduleKind)
	job.ScheduleExpression = strings.Join(strings.Fields(job.ScheduleExpression), " ")
	job.Timezone = strings.TrimSpace(job.Timezone)
	job.IntervalDuration = strings.TrimSpace(job.IntervalDuration)
	if job.ScheduleKind == ScheduleKindInterval {
		if interval, err := NormalizeIntervalDuration(job.IntervalDuration); err == nil {
			job.IntervalDuration = interval
		}
	} else {
		job.IntervalDuration = ""
	}
	job.Status = strings.ToLower(strings.TrimSpace(job.Status))
	if job.Status == "" {
		job.Status = StatusActive
	}
	job.SuppressionReason = strings.TrimSpace(job.SuppressionReason)
	job.Workdir = strings.TrimSpace(job.Workdir)
	job.Title = ExpectedTitle(job.CommandID, job.ScheduleID)
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	} else {
		job.CreatedAt = job.CreatedAt.UTC()
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = now
	} else {
		job.UpdatedAt = job.UpdatedAt.UTC()
	}
	return job
}

func normalizeScheduleKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return ScheduleKindCron
	}
	return kind
}

func (s *Store) jobsDir() string  { return filepath.Join(s.Root, "jobs") }
func (s *Store) runsDir() string  { return filepath.Join(s.Root, "runs") }
func (s *Store) locksDir() string { return filepath.Join(s.Root, "locks") }
func (s *Store) logsDir() string  { return filepath.Join(s.Root, "logs") }

func (s *Store) jobPath(scheduleID string) string {
	return filepath.Join(s.jobsDir(), scheduleID+".json")
}

func (s *Store) runPath(scheduleID string) string {
	return filepath.Join(s.runsDir(), scheduleID+".jsonl")
}

func (s *Store) lockPath(scheduleID string) string {
	return filepath.Join(s.locksDir(), scheduleID+".json")
}

func writeJSONAtomic(path string, value any, perm fs.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data, perm)
}

func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func isNotFound(err error) bool {
	return err != nil && ErrorCode(err) == "job_not_found"
}

func formatRequired(value string) string {
	if strings.TrimSpace(value) == "" {
		return "<empty>"
	}
	return fmt.Sprintf("%q", value)
}
