package sched

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	SchemaVersion = "sched/v1"

	StatusActive = "active"
	StatusPaused = "paused"

	ScheduleKindCron     = "cron"
	ScheduleKindInterval = "interval"

	RunSourceScheduled = "scheduled"
	RunSourceManual    = "manual"

	RunStatusSuccess = "success"
	RunStatusFailed  = "failed"
	RunStatusSkipped = "skipped"
)

type Job struct {
	SchemaVersion      string     `json:"schemaVersion"`
	ScheduleID         string     `json:"scheduleId"`
	TenantID           string     `json:"tenantId"`
	OpenCodeID         string     `json:"opencodeId"`
	AgentID            string     `json:"agentId"`
	AgentName          string     `json:"agentName"`
	CommandID          string     `json:"commandId"`
	CommandName        string     `json:"commandName"`
	NotifyChannel      *string    `json:"notifyChannel,omitempty"`
	NotifyTo           *string    `json:"notifyTo,omitempty"`
	ScheduleKind       string     `json:"scheduleKind"`
	ScheduleExpression string     `json:"scheduleExpression,omitempty"`
	Timezone           string     `json:"timezone,omitempty"`
	IntervalDuration   string     `json:"intervalDuration,omitempty"`
	Status             string     `json:"status"`
	TimerSuppressed    bool       `json:"timerSuppressed,omitempty"`
	SuppressionReason  string     `json:"suppressionReason,omitempty"`
	Workdir            string     `json:"workdir"`
	Title              string     `json:"title"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
	LastRunAt          *time.Time `json:"lastRunAt,omitempty"`
	LastRunStatus      string     `json:"lastRunStatus,omitempty"`
}

type RunRecord struct {
	RunID       string     `json:"runId"`
	ScheduleID  string     `json:"scheduleId"`
	Source      string     `json:"source"`
	Status      string     `json:"status"`
	StartedAt   time.Time  `json:"startedAt"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	DurationMs  int64      `json:"durationMs,omitempty"`
	ExitCode    *int       `json:"exitCode,omitempty"`
	LogPath     string     `json:"logPath,omitempty"`
	Message     string     `json:"message,omitempty"`
	CommandLine []string   `json:"commandLine,omitempty"`
}

type Export struct {
	SchemaVersion string `json:"schemaVersion"`
	Jobs          []Job  `json:"jobs"`
}

type CommandError struct {
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	Argument   string   `json:"argument,omitempty"`
	Received   string   `json:"received,omitempty"`
	Expected   string   `json:"expected,omitempty"`
	Hint       string   `json:"hint,omitempty"`
	Examples   []string `json:"examples,omitempty"`
	Suggestion string   `json:"suggestion,omitempty"`
	Usage      string   `json:"usage,omitempty"`
	Err        error    `json:"-"`
}

type CommandErrorOption func(*CommandError)

func (e *CommandError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *CommandError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *CommandError) WithDiagnostics(opts ...CommandErrorOption) *CommandError {
	if e == nil {
		return nil
	}
	for _, opt := range opts {
		if opt != nil {
			opt(e)
		}
	}
	return e
}

func NewCommandError(code string, message string, opts ...CommandErrorOption) *CommandError {
	return (&CommandError{Code: code, Message: message}).WithDiagnostics(opts...)
}

func Errorf(code string, format string, args ...any) *CommandError {
	return NewCommandError(code, fmt.Sprintf(format, args...))
}

func WrapError(code string, err error, format string, args ...any) *CommandError {
	if err == nil {
		return Errorf(code, format, args...)
	}
	return NewCommandError(code, fmt.Sprintf(format, args...)).withErr(err)
}

func AnnotateError(err error, opts ...CommandErrorOption) error {
	if err == nil {
		return nil
	}
	var commandErr *CommandError
	if errors.As(err, &commandErr) && commandErr != nil {
		return commandErr.clone().WithDiagnostics(opts...)
	}
	return NewCommandError(ErrorCode(err), ErrorMessage(err), opts...).withErr(err)
}

func WithArgument(argument string) CommandErrorOption {
	return func(e *CommandError) {
		e.Argument = strings.TrimSpace(argument)
	}
}

func WithReceived(received string) CommandErrorOption {
	return func(e *CommandError) {
		e.Received = strings.TrimSpace(received)
	}
}

func WithExpected(expected string) CommandErrorOption {
	return func(e *CommandError) {
		e.Expected = strings.TrimSpace(expected)
	}
}

func WithHint(hint string) CommandErrorOption {
	return func(e *CommandError) {
		e.Hint = strings.TrimSpace(hint)
	}
}

func WithExamples(examples ...string) CommandErrorOption {
	return func(e *CommandError) {
		e.Examples = nil
		for _, example := range examples {
			if example = strings.TrimSpace(example); example != "" {
				e.Examples = append(e.Examples, example)
			}
		}
	}
}

func WithSuggestion(suggestion string) CommandErrorOption {
	return func(e *CommandError) {
		e.Suggestion = strings.TrimSpace(suggestion)
	}
}

func WithUsage(usage string) CommandErrorOption {
	return func(e *CommandError) {
		e.Usage = strings.TrimSpace(usage)
	}
}

func (e *CommandError) clone() *CommandError {
	if e == nil {
		return nil
	}
	clone := *e
	if e.Examples != nil {
		clone.Examples = append([]string(nil), e.Examples...)
	}
	return &clone
}

func (e *CommandError) withErr(err error) *CommandError {
	if e == nil {
		return nil
	}
	e.Err = err
	return e
}

func ErrorCode(err error) string {
	var commandErr *CommandError
	if errors.As(err, &commandErr) && strings.TrimSpace(commandErr.Code) != "" {
		return commandErr.Code
	}
	return "internal_error"
}

func ErrorMessage(err error) string {
	var commandErr *CommandError
	if errors.As(err, &commandErr) && strings.TrimSpace(commandErr.Message) != "" {
		return commandErr.Message
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func ErrorDetails(err error) CommandError {
	var commandErr *CommandError
	if errors.As(err, &commandErr) && commandErr != nil {
		details := commandErr.clone()
		details.Err = nil
		if strings.TrimSpace(details.Code) == "" {
			details.Code = "internal_error"
		}
		if strings.TrimSpace(details.Message) == "" {
			details.Message = ErrorMessage(err)
		}
		return *details
	}
	return CommandError{Code: ErrorCode(err), Message: ErrorMessage(err)}
}

func RenderError(err error) string {
	details := ErrorDetails(err)
	var b strings.Builder
	b.WriteString(details.Message)
	appendDiagnosticLine(&b, "hint", details.Hint)
	appendDiagnosticLine(&b, "suggestion", details.Suggestion)
	appendDiagnosticLine(&b, "expected", details.Expected)
	for _, example := range details.Examples {
		appendDiagnosticLine(&b, "example", example)
	}
	appendDiagnosticLine(&b, "usage", details.Usage)
	return b.String()
}

func appendDiagnosticLine(b *strings.Builder, label string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	b.WriteString(label)
	b.WriteString(": ")
	b.WriteString(value)
}

func ExpectedTitle(commandID, scheduleID string) string {
	return "ci-command:" + strings.TrimSpace(commandID) + ":" + strings.TrimSpace(scheduleID)
}
