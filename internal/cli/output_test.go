package cli

import (
	"strings"
	"testing"
)

func TestRenderTableAlignsDeterministically(t *testing.T) {
	got := renderTable(
		[]string{"ID", "STATUS", "TARGET"},
		[][]string{{"sched-1", "active", "Daily-Backup @ build-agent"}, {"s2", "paused", "Ops @ syslog"}},
		"No rows.",
	)
	want := strings.Join([]string{
		"ID       STATUS  TARGET",
		"sched-1  active  Daily-Backup @ build-agent",
		"s2       paused  Ops @ syslog",
	}, "\n")
	if got != want {
		t.Fatalf("renderTable() =\n%s\nwant=\n%s", got, want)
	}
}

func TestRenderTableEmptyStateUsesTerminalText(t *testing.T) {
	got := renderTable([]string{"ID", "STATUS"}, nil, "No schedules found.")
	if got != "No schedules found." {
		t.Fatalf("empty renderTable() = %q", got)
	}
}
