package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func usageProgress(utilization float64, width int) string {
	width = max(1, width)
	ratio := math.Min(1, math.Max(0, utilization/100))
	whole := int(math.Floor(ratio * float64(width)))
	result := strings.Repeat("█", whole)
	if whole < width {
		blocks := []string{" ", "▏", "▎", "▍", "▌", "▋", "▊", "▉", "█"}
		middle := int(math.Floor((ratio*float64(width) - float64(whole)) * float64(len(blocks))))
		result += blocks[min(middle, 8)] + strings.Repeat(" ", width-whole-1)
	}
	return result
}
func usageProgressColor(text, theme string) string {
	colors := map[string][2]string{
		"light": {"38;2;87;105;247", "48;2;39;47;111"}, "dark": {"38;2;177;185;249", "48;2;80;83;112"},
		"light-daltonized": {"38;2;51;102;255", "48;2;23;46;114"}, "dark-daltonized": {"38;2;153;204;255", "48;2;69;92;115"},
		"light-ansi": {"33", "40"}, "dark-ansi": {"33", "47"},
	}
	if color, ok := colors[theme]; ok {
		return "\x1b[" + color[0] + "m\x1b[" + color[1] + "m" + text + "\x1b[39m\x1b[49m"
	}
	return text
}
func usageResetText(timestamp string, now time.Time, zone string, showTime bool) string {
	date, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return "Invalid Date"
	}
	location := now.Location()
	if zone != "" {
		if parsed, err := time.LoadLocation(zone); err == nil {
			location = parsed
		}
	}
	date = date.In(location)
	now = now.In(location)
	hour := date.Hour() % 12
	if hour == 0 {
		hour = 12
	}
	meridian := "am"
	if date.Hour() >= 12 {
		meridian = "pm"
	}
	clock := fmt.Sprintf("%d%s", hour, meridian)
	if date.Minute() != 0 {
		clock = fmt.Sprintf("%d:%02d%s", hour, date.Minute(), meridian)
	}
	if date.Sub(now) > 24*time.Hour {
		day := date.Format("Jan 2")
		if date.Year() != now.Year() {
			day += fmt.Sprintf(", %d", date.Year())
		}
		if showTime {
			day += ", " + clock
		}
		clock = day
	}
	if zone == "" {
		zone = location.String()
	}
	return clock + " (" + zone + ")"
}
func usageLimitLines(title string, limit protocol.JSONObject, width int, now time.Time, zone string, showTime bool, extra string, theme ...string) []string {
	if string(limit.Get("utilization")) == "null" {
		return nil
	}
	var percent float64
	_ = json.Unmarshal(limit.Get("utilization"), &percent)
	subtext := ""
	if reset := limit.String("resets_at"); reset != "" {
		subtext = "Resets " + usageResetText(reset, now, zone, showTime)
	}
	if extra != "" {
		if subtext != "" {
			subtext = extra + " · " + subtext
		} else {
			subtext = extra
		}
	}
	used := fmt.Sprintf("%.0f%% used", math.Floor(percent))
	progress := func(width int) string {
		bar := usageProgress(percent, width)
		if len(theme) > 0 {
			bar = usageProgressColor(bar, theme[0])
		}
		return bar
	}
	if width >= 62 {
		rows := []string{title, progress(50) + " " + used}
		if subtext != "" {
			rows = append(rows, subtext)
		}
		return rows
	}
	if subtext != "" {
		title += " · " + subtext
	}
	rows := exportWrapWords(title, width)
	rows = append(rows, progress(width), used)
	return rows
}

// UsageVisualization receives actual cached identity + fetched utilization.
// It never guesses subscription/availability from a selected model name.
func UsageVisualization(encoded json.RawMessage, columns int, theme ...string) []string {
	var presentation protocol.JSONObject
	if presentation.UnmarshalJSON(encoded) != nil {
		return nil
	}
	if failure := presentation.String("error"); failure != "" {
		return []string{"Error: " + failure, "", "r to retry · Esc to cancel"}
	}
	value, _ := presentation.Object("value")
	if string(presentation.Get("loading")) == "true" || string(value.Get("utilization")) == "null" || len(value.Get("utilization")) == 0 {
		return []string{"Loading usage data…", "", "Esc to cancel"}
	}
	data, _ := value.Object("utilization")
	subscription := value.String("subscriptionType")
	now, _ := time.Parse(time.RFC3339Nano, value.String("now"))
	zone := value.String("timezone")
	width := max(1, min(columns-2, 80))
	rows := []string{}
	foundLimit := false
	for _, item := range []struct{ key, title string }{{"five_hour", "Current session"}, {"seven_day", "Current week (all models)"}, {"seven_day_sonnet", "Current week (Sonnet only)"}} {
		if item.key == "seven_day_sonnet" && subscription != "max" && subscription != "team" && subscription != "" {
			continue
		}
		if limit, ok := data.Object(item.key); ok {
			foundLimit = true
			section := usageLimitLines(item.title, limit, width, now, zone, true, "", theme...)
			if len(section) > 0 {
				if len(rows) > 0 {
					rows = append(rows, "")
				}
				rows = append(rows, section...)
			}
		}
	}
	if !foundLimit {
		rows = append(rows, "/usage is only available for subscription plans.")
	}
	if extra, ok := data.Object("extra_usage"); ok && (subscription == "pro" || subscription == "max") {
		section := []string{}
		if string(extra.Get("is_enabled")) != "true" {
			if string(value.Get("extraUsageCommandEnabled")) == "true" {
				section = append([]string{"Extra usage"}, exportWrapWords("Extra usage not enabled · /extra-usage to enable", width)...)
			}
		} else if string(extra.Get("monthly_limit")) == "null" {
			section = []string{"Extra usage", "Unlimited"}
		} else {
			var used, limit, percent float64
			if json.Unmarshal(extra.Get("used_credits"), &used) == nil && json.Unmarshal(extra.Get("monthly_limit"), &limit) == nil && json.Unmarshal(extra.Get("utilization"), &percent) == nil && string(extra.Get("used_credits")) != "null" && string(extra.Get("utilization")) != "null" {
				location := now.Location()
				if zone != "" {
					if parsed, err := time.LoadLocation(zone); err == nil {
						location = parsed
					}
				}
				local := now.In(location)
				reset := time.Date(local.Year(), local.Month()+1, 1, 0, 0, 0, 0, location)
				section = usageLimitLines("Extra usage", protocol.NewJSONObject(protocol.JSONField{Name: "utilization", Value: extra.Get("utilization")}, protocol.JSONField{Name: "resets_at", Value: visualQuote(reset.Format(time.RFC3339Nano))}), width, now, zone, false, fmt.Sprintf("$%.2f / $%.2f spent", used/100, limit/100), theme...)
			}
		}
		if len(section) > 0 {
			if len(rows) > 0 {
				rows = append(rows, "")
			}
			rows = append(rows, section...)
		}
	}
	if grant, ok := value.Object("overageCreditGrant"); ok && string(grant.Get("available")) == "true" && string(grant.Get("granted")) != "true" && strings.EqualFold(grant.String("currency"), "USD") && string(grant.Get("amount_minor_units")) != "null" {
		var amount float64
		if json.Unmarshal(grant.Get("amount_minor_units"), &amount) == nil {
			dollars := amount / 100
			label := fmt.Sprintf("$%.2f", dollars)
			if math.Trunc(dollars) == dollars {
				label = fmt.Sprintf("$%.0f", dollars)
			}
			rows = append(rows, "", exportTruncate(label+" in extra usage for third-party apps · /extra-usage", width, true))
		}
	}
	return append(rows, "", "Esc to cancel")
}
func visualQuote(text string) json.RawMessage { raw, _ := json.Marshal(text); return raw }
