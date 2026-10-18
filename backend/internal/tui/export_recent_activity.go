package tui

import (
	"math"
	"strconv"
	"strings"
)

func exportActivityAgo(modified, now float64) string {
	seconds := math.Trunc((modified - now) / 1000)
	for _, interval := range []struct {
		seconds float64
		unit    string
	}{{31536000, "y"}, {2592000, "mo"}, {604800, "w"}, {86400, "d"}, {3600, "h"}, {60, "m"}, {1, "s"}} {
		if math.Abs(seconds) >= interval.seconds {
			number := math.Trunc(seconds / interval.seconds)
			if number < 0 {
				return strconv.FormatFloat(-number, 'f', 0, 64) + interval.unit + " ago"
			}
			return "in " + strconv.FormatFloat(number, 'f', 0, 64) + interval.unit
		}
	}
	return "0s ago"
}

func exportActivityWidth(value ConversationExportPresentation) (width, timestamp int) {
	width = exportWidth("Recent activity")
	if len(value.Activities) == 0 {
		return max(width, exportWidth("No recent activity")), 0
	}
	for _, activity := range value.Activities {
		timestamp = max(timestamp, exportWidth(exportActivityAgo(activity.ModifiedMillis, value.NowMillis)))
	}
	for _, activity := range value.Activities {
		width = max(width, exportWidth(activity.Description)+timestamp+2)
	}
	return max(width, exportWidth("/resume for more")), timestamp
}

func exportActivityRows(value ConversationExportPresentation, width int) []string {
	rows := []string{"Recent activity"}
	if len(value.Activities) == 0 {
		return append(rows, exportTruncate("No recent activity", width, true))
	}
	_, timestampWidth := exportActivityWidth(value)
	for _, activity := range value.Activities {
		stamp := exportActivityAgo(activity.ModifiedMillis, value.NowMillis)
		rows = append(rows, stamp+strings.Repeat(" ", max(0, timestampWidth-exportWidth(stamp)))+"  "+exportTruncate(activity.Description, max(10, width-timestampWidth-2), true))
	}
	return append(rows, exportTruncate("/resume for more", width, true))
}
