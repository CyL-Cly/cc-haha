package tui

import (
	"fmt"
	"math"
)

// The original useElapsedTime calls formatDuration with default display options.
func dreamDuration(milliseconds float64) string {
	ms := math.Max(0, milliseconds)
	if ms < 60000 {
		if ms == 0 {
			return "0s"
		}
		if ms < 1 {
			return fmt.Sprintf("%.1fs", ms/1000)
		}
		return fmt.Sprintf("%.0fs", math.Floor(ms/1000))
	}
	days := math.Floor(ms / 86400000)
	hours := math.Floor(math.Mod(ms, 86400000) / 3600000)
	minutes := math.Floor(math.Mod(ms, 3600000) / 60000)
	seconds := math.Floor(math.Mod(ms, 60000)/1000 + .5)
	if seconds == 60 {
		seconds = 0
		minutes++
	}
	if minutes == 60 {
		minutes = 0
		hours++
	}
	if hours == 24 {
		hours = 0
		days++
	}
	if days > 0 {
		return fmt.Sprintf("%.0fd %.0fh %.0fm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%.0fh %.0fm %.0fs", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%.0fm %.0fs", minutes, seconds)
	}
	return fmt.Sprintf("%.0fs", seconds)
}
