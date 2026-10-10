package cloudauth

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"
)

var awsRFC3339 = regexp.MustCompile(`^([0-9]{4})-([0-9]{2})-([0-9]{2})[tT]([0-9]{2}):([0-9]{2}):([0-9]{2})(?:\.([0-9]+))?[zZ]$`)

func parseAWSRFC3339(value string) (time.Time, error) {
	match := awsRFC3339.FindStringSubmatch(value)
	if match == nil {
		return time.Time{}, fmt.Errorf("Invalid RFC-3339 date-time value")
	}
	number := func(i int) int { v, _ := strconv.Atoi(match[i]); return v }
	year, month, day := number(1), number(2), number(3)
	for _, bound := range []struct {
		name            string
		value, min, max int
	}{{"month", month, 1, 12}, {"day", day, 1, 31}} {
		if bound.value < bound.min || bound.value > bound.max {
			return time.Time{}, fmt.Errorf("%s must be between %d and %d, inclusive", bound.name, bound.min, bound.max)
		}
	}
	maxDays := []int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}[month-1]
	if month == 2 && year%4 == 0 && (year%100 != 0 || year%400 == 0) {
		maxDays = 29
	}
	if day > maxDays {
		return time.Time{}, fmt.Errorf("Invalid day for %s in %d: %d", time.Month(month).String()[:3], year, day)
	}
	hours, minutes, seconds := number(4), number(5), number(6)
	for _, bound := range []struct {
		name            string
		value, min, max int
	}{{"hour", hours, 0, 23}, {"minute", minutes, 0, 59}, {"seconds", seconds, 0, 60}} {
		if bound.value < bound.min || bound.value > bound.max {
			return time.Time{}, fmt.Errorf("%s must be between %d and %d, inclusive", bound.name, bound.min, bound.max)
		}
	}
	fraction := 0.
	if match[7] != "" {
		fraction, _ = strconv.ParseFloat("0."+match[7], 64)
	}
	// Date.UTC keeps its legacy 0..99 year adjustment; day validation above runs
	// against the unadjusted year exactly as Smithy's buildDate does.
	if year < 100 {
		year += 1900
	}
	return time.Date(year, time.Month(month), day, hours, minutes, seconds, int(math.Trunc(fraction*1000))*int(time.Millisecond), time.UTC), nil
}
