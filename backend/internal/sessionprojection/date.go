package sessionprojection

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

var ErrUnsupportedDate = errors.New("session projection Date.parse profile does not support legacy or local-timezone date strings")
var isoDate = regexp.MustCompile(`^([+-][0-9]{6}|[0-9]{4})(?:-([0-9]{2})(?:-([0-9]{2}))?)?(?:[Tt]([0-9]{2}):([0-9]{2})(?::([0-9]{2})(?:\.([0-9]+))?)?([Zz]|[+-][0-9]{2}:?[0-9]{2})?)?$`)

// ParseDateUTC is a real Go parser for declared ECMAScript ISO date-only and
// explicit-offset date-time strings, and RFC1123/822 strings with GMT/UTC or
// numeric offsets. Unsupported legacy/local forms remain explicit migration
// work; fixtures never inject Source-parsed milliseconds into this boundary.
func ParseDateUTC(units []uint16) (float64, bool, error) {
	value := string(utf16.Decode(units))
	if value == "" {
		return 0, false, nil
	}
	if match := isoDate.FindStringSubmatch(value); match != nil {
		number := func(index, fallback int) int {
			if match[index] == "" {
				return fallback
			}
			result, _ := strconv.Atoi(match[index])
			return result
		}
		year, month, day := number(1, 0), number(2, 1), number(3, 1)
		hour, minute, second := number(4, 0), number(5, 0), number(6, 0)
		millis := 0
		if fraction := match[7]; fraction != "" {
			fraction += "000"
			millis, _ = strconv.Atoi(fraction[:3])
		}
		if match[1] == "-000000" || month < 1 || month > 12 || day < 1 || day > 31 || hour > 24 || minute > 59 || second > 59 || hour == 24 && (minute != 0 || second != 0 || millis != 0) || match[4] != "" && (match[2] == "" || match[3] == "") {
			return 0, false, nil
		}
		zone := match[8]
		if match[4] != "" && zone == "" {
			return 0, false, ErrUnsupportedDate
		}
		offset := 0
		if zone != "" && zone != "Z" && zone != "z" {
			zoneHour, _ := strconv.Atoi(zone[1:3])
			zoneMinute, _ := strconv.Atoi(strings.ReplaceAll(zone[3:], ":", ""))
			if zoneHour > 23 || zoneMinute > 59 {
				return 0, false, nil
			}
			offset = (zoneHour*60 + zoneMinute) * 60
			if zone[0] == '-' {
				offset = -offset
			}
		}
		timestamp := time.Date(year, time.Month(month), day, hour, minute, second, millis*int(time.Millisecond), time.UTC).UnixMilli() - int64(offset)*1000
		if timestamp > 8640000000000000 || timestamp < -8640000000000000 {
			return 0, false, nil
		}
		return float64(timestamp), true, nil
	}
	for _, layout := range []string{
		"Mon, 02 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 -0700",
		"Mon, 02 Jan 2006 15:04:05 GMT", "Mon, 2 Jan 2006 15:04:05 GMT",
		"Mon, 02 Jan 2006 15:04:05 UTC", "Mon, 2 Jan 2006 15:04:05 UTC",
		"02 Jan 06 15:04 -0700", "02 Jan 06 15:04 GMT", "02 Jan 06 15:04 UTC",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return float64(parsed.UnixMilli()), true, nil
		}
	}
	return 0, false, ErrUnsupportedDate
}
