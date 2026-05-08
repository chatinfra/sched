package sched

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type cronField struct {
	all    bool
	values []int
}

func CronToSystemdCalendars(expr, timezone string) ([]string, error) {
	if strings.TrimSpace(timezone) == "" {
		return nil, Errorf("invalid_timezone", "timezone is required")
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return nil, WrapError("invalid_timezone", err, "timezone %q is invalid", timezone)
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, Errorf("invalid_cron", "scheduleExpression must be a 5-field cron expression")
	}
	minute, err := parseCronField(fields[0], 0, 59, false)
	if err != nil {
		return nil, WrapError("invalid_cron", err, "invalid minute field %q", fields[0])
	}
	hour, err := parseCronField(fields[1], 0, 23, false)
	if err != nil {
		return nil, WrapError("invalid_cron", err, "invalid hour field %q", fields[1])
	}
	dayOfMonth, err := parseCronField(fields[2], 1, 31, false)
	if err != nil {
		return nil, WrapError("invalid_cron", err, "invalid day-of-month field %q", fields[2])
	}
	month, err := parseCronField(fields[3], 1, 12, false)
	if err != nil {
		return nil, WrapError("invalid_cron", err, "invalid month field %q", fields[3])
	}
	dayOfWeek, err := parseCronField(fields[4], 0, 7, true)
	if err != nil {
		return nil, WrapError("invalid_cron", err, "invalid day-of-week field %q", fields[4])
	}

	minutePart := systemdNumericList(minute, 2)
	hourPart := systemdNumericList(hour, 2)
	monthPart := systemdNumericList(month, 0)
	domPart := systemdNumericList(dayOfMonth, 0)
	dowPart := systemdWeekdayList(dayOfWeek)

	render := func(dow string, dom string) string {
		date := fmt.Sprintf("*-%s-%s", monthPart, dom)
		clock := fmt.Sprintf("%s:%s:00", hourPart, minutePart)
		if dow != "" {
			return fmt.Sprintf("%s %s %s %s", dow, date, clock, timezone)
		}
		return fmt.Sprintf("%s %s %s", date, clock, timezone)
	}

	// Cron treats day-of-month and day-of-week as an OR when both are
	// constrained. systemd calendar expressions are AND expressions, so emit two
	// OnCalendar entries to preserve the cron behavior.
	if !dayOfMonth.all && !dayOfWeek.all {
		return []string{render("", domPart), render(dowPart, "*")}, nil
	}
	if !dayOfWeek.all {
		return []string{render(dowPart, domPart)}, nil
	}
	return []string{render("", domPart)}, nil
}

func parseCronField(raw string, min int, max int, sundayAlias bool) (cronField, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return cronField{}, fmt.Errorf("field is empty")
	}
	if raw == "*" {
		return cronField{all: true}, nil
	}
	seen := map[int]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return cronField{}, fmt.Errorf("empty list item")
		}
		base := part
		step := 1
		if before, after, ok := strings.Cut(part, "/"); ok {
			base = before
			parsedStep, err := strconv.Atoi(after)
			if err != nil || parsedStep <= 0 {
				return cronField{}, fmt.Errorf("invalid step %q", after)
			}
			step = parsedStep
		}
		start, end, err := parseCronRange(base, min, max)
		if err != nil {
			return cronField{}, err
		}
		for value := start; value <= end; value += step {
			if sundayAlias && value == 7 {
				value = 0
			}
			if value < min || value > max || (sundayAlias && value == 7) {
				return cronField{}, fmt.Errorf("value %d out of range %d-%d", value, min, max)
			}
			seen[value] = true
		}
	}
	values := make([]int, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}
	sort.Ints(values)
	expectedAll := max - min + 1
	if sundayAlias {
		// 0 and 7 both mean Sunday, so the unique full set is 0..6.
		expectedAll = 7
	}
	return cronField{all: len(values) == expectedAll, values: values}, nil
}

func parseCronRange(raw string, min int, max int) (int, int, error) {
	if raw == "*" {
		return min, max, nil
	}
	if before, after, ok := strings.Cut(raw, "-"); ok {
		start, err := strconv.Atoi(before)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range start %q", before)
		}
		end, err := strconv.Atoi(after)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range end %q", after)
		}
		if start > end {
			return 0, 0, fmt.Errorf("range start %d is greater than end %d", start, end)
		}
		if start < min || end > max {
			return 0, 0, fmt.Errorf("range %d-%d out of range %d-%d", start, end, min, max)
		}
		return start, end, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid value %q", raw)
	}
	if value < min || value > max {
		return 0, 0, fmt.Errorf("value %d out of range %d-%d", value, min, max)
	}
	return value, value, nil
}

func systemdNumericList(field cronField, width int) string {
	if field.all {
		return "*"
	}
	parts := make([]string, 0, len(field.values))
	for _, value := range field.values {
		if width > 0 {
			parts = append(parts, fmt.Sprintf("%0*d", width, value))
		} else {
			parts = append(parts, strconv.Itoa(value))
		}
	}
	return strings.Join(parts, ",")
}

func systemdWeekdayList(field cronField) string {
	if field.all {
		return ""
	}
	names := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	parts := make([]string, 0, len(field.values))
	for _, value := range field.values {
		parts = append(parts, names[value])
	}
	return strings.Join(parts, ",")
}
