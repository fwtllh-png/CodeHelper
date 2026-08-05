package automation

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Frequency is the supported RFC 5545 recurrence frequency subset.
type Frequency string

const (
	FrequencyHourly Frequency = "HOURLY"
	FrequencyWeekly Frequency = "WEEKLY"
)

// Rule is CodeHelper's deliberately small, deterministic RRULE subset. All
// recurrence arithmetic is performed in UTC.
type Rule struct {
	Frequency Frequency
	Interval  int
	ByDay     []time.Weekday
}

// ParseRRULE accepts FREQ=HOURLY with optional INTERVAL, and FREQ=WEEKLY with
// optional INTERVAL and BYDAY. No local-time or DST-dependent fields are
// accepted.
func ParseRRULE(value string) (Rule, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(strings.ToUpper(value), "RRULE:")
	if value == "" {
		return Rule{}, errors.New("automation RRULE is required")
	}
	fields := make(map[string]string)
	for _, component := range strings.Split(value, ";") {
		key, raw, found := strings.Cut(component, "=")
		if !found || key == "" || raw == "" {
			return Rule{}, fmt.Errorf("invalid RRULE component %q", component)
		}
		if _, duplicate := fields[key]; duplicate {
			return Rule{}, fmt.Errorf("duplicate RRULE field %s", key)
		}
		switch key {
		case "FREQ", "INTERVAL", "BYDAY":
			fields[key] = raw
		default:
			return Rule{}, fmt.Errorf("unsupported RRULE field %s", key)
		}
	}

	rule := Rule{Frequency: Frequency(fields["FREQ"]), Interval: 1}
	if raw := fields["INTERVAL"]; raw != "" {
		interval, err := strconv.Atoi(raw)
		if err != nil || interval <= 0 {
			return Rule{}, errors.New("RRULE INTERVAL must be a positive integer")
		}
		rule.Interval = interval
	}
	switch rule.Frequency {
	case FrequencyHourly:
		if fields["BYDAY"] != "" {
			return Rule{}, errors.New("RRULE BYDAY is only supported for WEEKLY")
		}
	case FrequencyWeekly:
		if raw := fields["BYDAY"]; raw != "" {
			seen := make(map[time.Weekday]bool)
			for _, token := range strings.Split(raw, ",") {
				day, ok := parseWeekday(token)
				if !ok {
					return Rule{}, fmt.Errorf("unsupported RRULE weekday %q", token)
				}
				if !seen[day] {
					rule.ByDay = append(rule.ByDay, day)
					seen[day] = true
				}
			}
			sort.Slice(rule.ByDay, func(i, j int) bool {
				return mondayIndex(rule.ByDay[i]) < mondayIndex(rule.ByDay[j])
			})
		}
	default:
		return Rule{}, fmt.Errorf("unsupported RRULE frequency %q", rule.Frequency)
	}
	return rule, nil
}

// Canonical returns a stable representation suitable for persistence.
func (r Rule) Canonical() string {
	value := "FREQ=" + string(r.Frequency)
	if r.Interval != 1 {
		value += ";INTERVAL=" + strconv.Itoa(r.Interval)
	}
	if len(r.ByDay) > 0 {
		days := make([]string, 0, len(r.ByDay))
		for _, day := range r.ByDay {
			days = append(days, weekdayToken(day))
		}
		value += ";BYDAY=" + strings.Join(days, ",")
	}
	return value
}

// Next returns the first occurrence strictly after after. The creation anchor
// defines the phase forever; callers must never replace it with restart time.
func (r Rule) Next(anchor, after time.Time) time.Time {
	anchor = anchor.UTC()
	after = after.UTC()
	if after.Before(anchor) {
		return anchor
	}
	if r.Frequency == FrequencyHourly {
		period := time.Duration(r.Interval) * time.Hour
		steps := after.Sub(anchor)/period + 1
		return anchor.Add(steps * period)
	}

	days := r.ByDay
	if len(days) == 0 {
		days = []time.Weekday{anchor.Weekday()}
	}
	anchorWeek := mondayStart(anchor)
	week := int(after.Sub(anchorWeek) / (7 * 24 * time.Hour))
	if week < 0 {
		week = 0
	}
	week -= week % r.Interval
	timeOfDay := anchor.Sub(time.Date(
		anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, time.UTC,
	))
	for {
		start := anchorWeek.AddDate(0, 0, week*7)
		for _, day := range days {
			candidate := start.AddDate(0, 0, mondayIndex(day)).Add(timeOfDay)
			if !candidate.Before(anchor) && candidate.After(after) {
				return candidate
			}
		}
		week += r.Interval
	}
}

func mondayStart(value time.Time) time.Time {
	midnight := time.Date(
		value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC,
	)
	return midnight.AddDate(0, 0, -mondayIndex(value.Weekday()))
}

func mondayIndex(day time.Weekday) int {
	return (int(day) + 6) % 7
}

func parseWeekday(value string) (time.Weekday, bool) {
	switch value {
	case "MO":
		return time.Monday, true
	case "TU":
		return time.Tuesday, true
	case "WE":
		return time.Wednesday, true
	case "TH":
		return time.Thursday, true
	case "FR":
		return time.Friday, true
	case "SA":
		return time.Saturday, true
	case "SU":
		return time.Sunday, true
	default:
		return 0, false
	}
}

func weekdayToken(day time.Weekday) string {
	switch day {
	case time.Monday:
		return "MO"
	case time.Tuesday:
		return "TU"
	case time.Wednesday:
		return "WE"
	case time.Thursday:
		return "TH"
	case time.Friday:
		return "FR"
	case time.Saturday:
		return "SA"
	default:
		return "SU"
	}
}
