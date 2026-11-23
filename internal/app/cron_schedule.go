package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// cronCalendar converts five-field cron expressions to systemd OnCalendar
// entries. Enumerating field values avoids different cron/systemd step origins.
// When both date and weekday are restricted, cron's OR semantics require two
// OnCalendar entries (systemd combines them with AND within one expression).
func cronCalendar(expression string) ([]string, error) {
	aliases := map[string]string{
		"@hourly": "0 * * * *",
		"@daily": "0 0 * * *",
		"@midnight": "0 0 * * *",
		"@weekly": "0 0 * * 0",
		"@monthly": "0 0 1 * *",
		"@yearly": "0 0 1 1 *",
		"@annually": "0 0 1 1 *",
	}
	expression = strings.TrimSpace(expression)
	if alias, ok := aliases[expression]; ok {
		expression = alias
	}
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return nil, errors.New("schedule must have five cron fields: minute hour day month weekday")
	}
	bounds := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	var values [5][]int
	for i, field := range fields {
		v, err := expandCronField(field, bounds[i][0], bounds[i][1])
		if err != nil {
			return nil, fmt.Errorf("invalid cron field %d (%s): %w", i+1, field, err)
		}
		values[i] = v
	}
	// Cron treats both 0 and 7 as Sunday; normalize before forming weekdays.
	weekdays := make(map[int]bool)
	for _, day := range values[4] {
		weekdays[day%7] = true
	}
	weekdayNames := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	var weekdayParts []string
	for day, name := range weekdayNames {
		if weekdays[day] {
			weekdayParts = append(weekdayParts, name)
		}
	}
	dayPart := formatCronNumbers(values[2], 1, 31)
	monthPart := formatCronNumbers(values[3], 1, 12)
	hourPart := formatCronNumbers(values[1], 0, 23)
	minutePart := formatCronNumbers(values[0], 0, 59)
	timePart := hourPart + ":" + minutePart + ":00"
	date := "*-" + monthPart + "-" + dayPart
	weekday := strings.Join(weekdayParts, ",")
	dateRestricted := len(values[2]) != 31
	weekdayRestricted := len(weekdays) != 7
	if dateRestricted && weekdayRestricted {
		return []string{
			"*-" + monthPart + "-" + dayPart + " " + timePart,
			weekday + " *-" + monthPart + "-* " + timePart,
		}, nil
	}
	if weekdayRestricted {
		return []string{weekday + " " + date + " " + timePart}, nil
	}
	return []string{date + " " + timePart}, nil
}

func expandCronField(field string, min, max int) ([]int, error) {
	if field == "" {
		return nil, errors.New("empty field")
	}
	selected := make(map[int]bool)
	for _, part := range strings.Split(field, ",") {
		if part == "" {
			return nil, errors.New("empty list element")
		}
		step := 1
		hasStep := strings.Contains(part, "/")
		if hasStep {
			pieces := strings.Split(part, "/")
			if len(pieces) != 2 {
				return nil, errors.New("invalid step")
			}
			n, err := strconv.Atoi(pieces[1])
			if err != nil || n < 1 || n > max-min+1 {
				return nil, errors.New("step out of range")
			}
			step = n
			part = pieces[0]
		}
		start, end := min, max
		if part != "*" {
			if strings.Contains(part, "-") {
				bounds := strings.Split(part, "-")
				if len(bounds) != 2 {
					return nil, errors.New("invalid range")
				}
				var err error
				start, err = strconv.Atoi(bounds[0])
				if err != nil {
					return nil, errors.New("invalid range start")
				}
				end, err = strconv.Atoi(bounds[1])
				if err != nil {
					return nil, errors.New("invalid range end")
				}
			} else {
				n, err := strconv.Atoi(part)
				if err != nil {
					return nil, errors.New("invalid value")
				}
				start = n
				if !hasStep {
					end = n
				}
			}
		}
		if start < min || end > max || start > end {
			return nil, errors.New("value out of range")
		}
		for n := start; n <= end; n += step {
			selected[n] = true
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("no matching values")
	}
	var result []int
	for n := min; n <= max; n++ {
		if selected[n] {
			result = append(result, n)
		}
	}
	return result, nil
}

func formatCronNumbers(values []int, min, max int) string {
	if len(values) == max-min+1 {
		return "*"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%02d", value))
	}
	return strings.Join(parts, ",")
}
