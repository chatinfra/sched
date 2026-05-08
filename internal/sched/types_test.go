package sched

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCommandErrorDiagnosticsJSONAndCompatibility(t *testing.T) {
	cause := errors.New("parse duration")
	err := WrapError("invalid_interval", cause, "interval duration %q is invalid", "1 minute").WithDiagnostics(
		WithArgument("--every"),
		WithReceived("1 minute"),
		WithExpected("a single Go-style duration such as 1m, 60s, or 2h"),
		WithHint("Use one duration token; for one minute pass --every 1m."),
		WithExamples("--every 1m", "--every 60s"),
		WithSuggestion("--every 1m"),
		WithUsage("sched create --every <duration>"),
	)

	if ErrorCode(err) != "invalid_interval" {
		t.Fatalf("ErrorCode() = %q", ErrorCode(err))
	}
	if ErrorMessage(err) != `interval duration "1 minute" is invalid` {
		t.Fatalf("ErrorMessage() = %q", ErrorMessage(err))
	}
	if !errors.Is(err, cause) {
		t.Fatalf("wrapped cause was not preserved")
	}

	data, jsonErr := json.Marshal(ErrorDetails(err))
	if jsonErr != nil {
		t.Fatalf("Marshal(CommandError) error = %v", jsonErr)
	}
	var got struct {
		Code       string   `json:"code"`
		Message    string   `json:"message"`
		Argument   string   `json:"argument"`
		Received   string   `json:"received"`
		Expected   string   `json:"expected"`
		Hint       string   `json:"hint"`
		Examples   []string `json:"examples"`
		Suggestion string   `json:"suggestion"`
		Usage      string   `json:"usage"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal(CommandError) error = %v\n%s", err, data)
	}
	if got.Code != "invalid_interval" || got.Message == "" || got.Argument != "--every" || got.Received != "1 minute" || got.Expected == "" || got.Hint == "" || got.Suggestion != "--every 1m" || got.Usage == "" {
		t.Fatalf("diagnostic JSON = %#v", got)
	}
	if len(got.Examples) != 2 || got.Examples[0] != "--every 1m" || got.Examples[1] != "--every 60s" {
		t.Fatalf("examples = %#v", got.Examples)
	}
}

func TestCommandErrorJSONOmitsEmptyDiagnostics(t *testing.T) {
	data, err := json.Marshal(ErrorDetails(Errorf("missing_required_flag", "--command is required")))
	if err != nil {
		t.Fatalf("Marshal(CommandError) error = %v", err)
	}
	jsonText := string(data)
	for _, omitted := range []string{"argument", "received", "expected", "hint", "examples", "suggestion", "usage", "Err"} {
		if strings.Contains(jsonText, omitted) {
			t.Fatalf("JSON %s unexpectedly contains %q", jsonText, omitted)
		}
	}
	if !strings.Contains(jsonText, `"code":"missing_required_flag"`) || !strings.Contains(jsonText, `"message":"--command is required"`) {
		t.Fatalf("JSON missing compatibility fields: %s", jsonText)
	}
}

func TestRenderErrorDiagnosticsDeterministic(t *testing.T) {
	err := NewCommandError("invalid_interval", `interval duration "1 minute" is invalid`,
		WithHint("Use one duration token; for one minute pass --every 1m."),
		WithSuggestion("--every 1m"),
		WithExpected("a single Go-style duration such as 1m, 60s, or 2h"),
		WithExamples("--every 1m", "--every 60s"),
		WithUsage("sched create --every <duration>"),
	)

	want := strings.Join([]string{
		`interval duration "1 minute" is invalid`,
		"hint: Use one duration token; for one minute pass --every 1m.",
		"suggestion: --every 1m",
		"expected: a single Go-style duration such as 1m, 60s, or 2h",
		"example: --every 1m",
		"example: --every 60s",
		"usage: sched create --every <duration>",
	}, "\n")
	if got := RenderError(err); got != want {
		t.Fatalf("RenderError() =\n%s\nwant=\n%s", got, want)
	}
}

func TestAnnotateErrorPreservesTypedError(t *testing.T) {
	cause := errors.New("parse duration")
	base := WrapError("invalid_interval", cause, "interval duration %q is invalid", "1 minute")
	annotated := AnnotateError(base, WithArgument("--every"), WithExamples("--every 1m"))

	if ErrorCode(annotated) != "invalid_interval" || ErrorMessage(annotated) != base.Message {
		t.Fatalf("annotated error code/message = %q/%q", ErrorCode(annotated), ErrorMessage(annotated))
	}
	if !errors.Is(annotated, cause) {
		t.Fatalf("annotated error does not unwrap cause")
	}
	details := ErrorDetails(annotated)
	if details.Argument != "--every" || len(details.Examples) != 1 || details.Examples[0] != "--every 1m" {
		t.Fatalf("annotated details = %#v", details)
	}
}
