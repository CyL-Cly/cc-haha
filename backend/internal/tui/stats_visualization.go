package tui

import (
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"
)

type StatsPresentation struct {
	Stats, AllTime    protocol.JSONObject
	Range, Tab, Theme string
	Now               time.Time
	Labels            map[string]string
}
type statsModelUsage struct {
	Name          string
	Input, Output float64
}

func visualNumber(o protocol.JSONObject, key string) float64 {
	var n float64
	_ = json.Unmarshal(o.Get(key), &n)
	return n
}
func visualModels(stats protocol.JSONObject) ([]statsModelUsage, float64) {
	var entries map[string]protocol.JSONObject
	_ = json.Unmarshal(stats.Get("modelUsage"), &entries)
	names := []string{}
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	result := []statsModelUsage{}
	total := float64(0)
	for _, name := range names {
		row := entries[name]
		input, output := visualNumber(row, "inputTokens"), visualNumber(row, "outputTokens")
		result = append(result, statsModelUsage{name, input, output})
		total += input + output
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Input+result[i].Output > result[j].Input+result[j].Output })
	return result, total
}
func visualFormat(n float64) string {
	scale, suffix := float64(1), ""
	for _, unit := range []struct {
		n float64
		s string
	}{{1e12, "t"}, {1e9, "b"}, {1e6, "m"}, {1e3, "k"}} {
		if n >= unit.n {
			scale, suffix = unit.n, unit.s
			break
		}
	}
	digits := 0
	if n >= 1000 {
		digits = 1
	}
	return strconv.FormatFloat(n/scale, 'f', digits, 64) + suffix
}
func visualDuration(ms float64) string {
	if ms < 60000 {
		if ms < 1 && ms != 0 {
			return fmt.Sprintf("%.1fs", ms/1000)
		}
		return fmt.Sprintf("%.0fs", math.Floor(ms/1000))
	}
	days, hours, minutes := int(ms/86400000), int(math.Mod(ms, 86400000)/3600000), int(math.Mod(ms, 3600000)/60000)
	seconds := int(math.Floor(math.Mod(ms, 60000)/1000 + .5))
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
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	return fmt.Sprintf("%dm %ds", minutes, seconds)
}
func visualHighlight(text string) string {
	return "\x1b[38;2;215;119;87m" + displayText(text) + "\x1b[0m"
}

func statsHeatmap(stats protocol.JSONObject, width int, now time.Time) []string {
	var activity []protocol.JSONObject
	_ = json.Unmarshal(stats.Get("dailyActivity"), &activity)
	if len(activity) == 0 {
		return nil
	}
	weeks := min(52, max(10, width-4))
	counts := []float64{}
	byDate := map[string]float64{}
	for _, day := range activity {
		n := visualNumber(day, "messageCount")
		byDate[day.String("date")] = n
		if n > 0 {
			counts = append(counts, n)
		}
	}
	sort.Float64s(counts)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start := today.AddDate(0, 0, -int(today.Weekday())-(weeks-1)*7)
	rows := make([]string, 7)
	months := []time.Month{}
	last := time.Month(0)
	day := start
	for week := 0; week < weeks; week++ {
		for weekday := 0; weekday < 7; weekday++ {
			if day.After(today) {
				rows[weekday] += " "
				day = day.AddDate(0, 0, 1)
				continue
			}
			if weekday == 0 && day.Month() != last {
				months = append(months, day.Month())
				last = day.Month()
			}
			n := byDate[day.Format("2006-01-02")]
			glyph := "·"
			color := "\x1b[90m"
			if n > 0 && len(counts) > 0 {
				color = "\x1b[38;2;218;119;86m"
				switch {
				case n >= counts[len(counts)*3/4]:
					glyph = "█"
				case n >= counts[len(counts)/2]:
					glyph = "▓"
				case n >= counts[len(counts)/4]:
					glyph = "▒"
				default:
					glyph = "░"
				}
			}
			rows[weekday] += color + glyph + "\x1b[0m"
			day = day.AddDate(0, 0, 1)
		}
	}
	monthLabels := "    "
	labelWidth := weeks / max(1, len(months))
	for _, month := range months {
		name := month.String()[:3]
		monthLabels += name + strings.Repeat(" ", max(0, labelWidth-len(name)))
	}
	result := []string{monthLabels}
	for i, row := range rows {
		label := "   "
		if i == 1 || i == 3 || i == 5 {
			label = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}[i]
		}
		result = append(result, label+" "+row)
	}
	result = append(result, "", "    Less \x1b[38;2;218;119;86m░ ▒ ▓ █\x1b[0m More")
	return result
}

// statsPlot follows Source's asciichart axis/rounding and series overwrite
// order. Labels are cells, so multi-character labels keep Source's offset.
func statsPlot(series [][]float64, colors []string) []string {
	minValue, maxValue := series[0][0], series[0][0]
	width := 0
	for _, line := range series {
		width = max(width, len(line))
		for _, n := range line {
			minValue = math.Min(minValue, n)
			maxValue = math.Max(maxValue, n)
		}
	}
	delta := math.Abs(maxValue - minValue)
	ratio := float64(1)
	if delta != 0 {
		ratio = 8 / delta
	}
	round := func(n float64) int { return int(math.Floor(n + .5)) }
	low, high := round(minValue*ratio), round(maxValue*ratio)
	rows := high - low
	width += 3
	grid := make([][]string, rows+1)
	for y := range grid {
		grid[y] = make([]string, width)
		for x := range grid[y] {
			grid[y][x] = " "
		}
	}
	for y := low; y <= high; y++ {
		n := float64(y)
		if rows > 0 {
			n = maxValue - float64(y-low)*delta/float64(rows)
		}
		label := fmt.Sprintf("%.0f", n)
		if n >= 1e6 {
			label = fmt.Sprintf("%.1fM", n/1e6)
		} else if n >= 1e3 {
			label = fmt.Sprintf("%.0fk", n/1e3)
		}
		label = strings.Repeat(" ", max(0, 6-len(label))) + label
		grid[y-low][0] = label
		grid[y-low][2] = "┤"
		if y == 0 {
			grid[y-low][2] = "┼"
		}
	}
	for j, line := range series {
		color := colors[j%len(colors)]
		paint := func(y, x int, g string) { grid[rows-y][x] = color + g + "\x1b[0m" }
		y0 := round(line[0]*ratio) - low
		paint(y0, 2, "┼")
		for x := 0; x < len(line)-1; x++ {
			a, b := round(line[x]*ratio)-low, round(line[x+1]*ratio)-low
			if a == b {
				paint(a, x+3, "─")
			} else {
				end, start := "╭", "╯"
				if a > b {
					end, start = "╰", "╮"
				}
				paint(b, x+3, end)
				paint(a, x+3, start)
				for y := min(a, b) + 1; y < max(a, b); y++ {
					paint(y, x+3, "│")
				}
			}
		}
	}
	result := []string{}
	for _, row := range grid {
		result = append(result, strings.Join(row, ""))
	}
	return result
}
func statsTokenChart(stats protocol.JSONObject, models []statsModelUsage, width int, theme string, labels map[string]string) []string {
	var days []protocol.JSONObject
	_ = json.Unmarshal(stats.Get("dailyModelTokens"), &days)
	if len(days) < 2 || len(models) == 0 {
		return nil
	}
	chartWidth := min(52, max(20, width-7))
	recent := []protocol.JSONObject{}
	if len(days) >= chartWidth {
		recent = days[len(days)-chartWidth:]
	} else {
		for _, day := range days {
			for i := 0; i < chartWidth/len(days); i++ {
				recent = append(recent, day)
			}
		}
	}
	series := [][]float64{}
	legend := []string{}
	colors := statsThemeColors(theme)
	for i, model := range models[:min(3, len(models))] {
		data := []float64{}
		positive := false
		for _, day := range recent {
			tokens, _ := day.Object("tokensByModel")
			n := visualNumber(tokens, model.Name)
			positive = positive || n > 0
			data = append(data, n)
		}
		if positive {
			series = append(series, data)
			name := model.Name
			if label := labels[name]; label != "" {
				name = label
			}
			legend = append(legend, colors[i]+"●\x1b[0m "+displayText(name))
		}
	}
	if len(series) == 0 {
		return nil
	}
	result := append([]string{"\x1b[1mTokens per Day\x1b[0m"}, statsPlot(series, colors)...)
	labelCount := min(4, max(2, len(recent)/8))
	step := (len(recent) - 6) / (labelCount - 1)
	if step == 0 {
		step = 1
	}
	xLabels := "       "
	position := 0
	for i := 0; i < labelCount; i++ {
		index := min(i*step, len(recent)-1)
		date, err := time.Parse("2006-01-02", recent[index].String("date"))
		label := recent[index].String("date")
		if err == nil {
			label = date.Format("Jan 2")
		}
		xLabels += strings.Repeat(" ", max(1, index-position)) + label
		position = index + len(label)
	}
	return append(result, "\x1b[90m"+xLabels+"\x1b[39m", strings.Join(legend, " · "), "")
}

func StatsVisualization(raw json.RawMessage, width int) []string {
	var p StatsPresentation
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	return statsVisualLines(p, width, false)
}
func StatsScreenshotANSI(p StatsPresentation) string {
	lines := statsVisualLines(p, 80, true)
	for len(lines) > 0 && strings.TrimSpace(displayText(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 {
		last := len(lines) - 1
		width := 70
		if p.Tab == "Models" {
			width = 80
		}
		lines[last] += strings.Repeat(" ", max(2, width-len([]rune(displayText(lines[last])))-6)) + "\x1b[90m/stats\x1b[0m"
	}
	return strings.Join(lines, "\n")
}
func statsVisualLines(p StatsPresentation, width int, screenshot bool) []string {
	stats := p.Stats
	models, total := visualModels(stats)
	label := func(name string) string {
		if value := p.Labels[name]; value != "" {
			return value
		}
		return name
	}
	if visualNumber(p.AllTime, "totalSessions") == 0 {
		return []string{"No Claude Code usage data available"}
	}
	lines := []string{}
	if p.Tab == "Models" {
		if len(models) == 0 {
			return []string{"No model usage data available"}
		}
		lines = append(lines, statsTokenChart(stats, models, width, p.Theme, p.Labels)...)
		limit := len(models)
		if screenshot {
			limit = min(3, limit)
			lines = append(lines, fmt.Sprintf("★ Favorite: \x1b[35m\x1b[1m%s\x1b[22m\x1b[39m · ◯ Total: \x1b[35m%s\x1b[39m tokens", displayText(label(models[0].Name)), visualFormat(total)), "")
		}
		for _, model := range models[:limit] {
			percentage := float64(0)
			if total != 0 {
				percentage = (model.Input + model.Output) / total * 100
			}
			lines = append(lines, fmt.Sprintf("● \x1b[1m%s\x1b[22m \x1b[90m(%.1f%%)\x1b[39m", displayText(label(model.Name)), percentage), fmt.Sprintf("\x1b[2m  In: %s · Out: %s\x1b[22m", visualFormat(model.Input), visualFormat(model.Output)))
		}
		return lines
	}
	heatmap := p.AllTime
	heatmapWidth := width
	if screenshot {
		heatmap = stats
		heatmapWidth = 56
	}
	lines = append(lines, statsHeatmap(heatmap, heatmapWidth, p.Now)...)
	if screenshot && len(lines) > 0 {
		lines = append(lines, "")
	}
	row := func(a, b, c, d string) {
		label := displayText(a) + ":"
		left := label + strings.Repeat(" ", max(0, 18-len(label))) + visualHighlight(b)
		other := displayText(c) + ":"
		lines = append(lines, left+strings.Repeat(" ", max(2, 40-len(displayText(left))))+other+strings.Repeat(" ", max(0, 18-len(other)))+visualHighlight(d))
	}
	if len(models) > 0 {
		row("Favorite model", label(models[0].Name), "Total tokens", visualFormat(total))
	}
	if screenshot {
		lines = append(lines, "")
	}
	longest := "N/A"
	if value, ok := stats.Object("longestSession"); ok {
		longest = visualDuration(visualNumber(value, "duration"))
	}
	row("Sessions", visualFormat(visualNumber(stats, "totalSessions")), "Longest session", longest)
	streaks, _ := stats.Object("streaks")
	allStreaks, _ := p.AllTime.Object("streaks")
	current := visualNumber(allStreaks, "currentStreak")
	if screenshot {
		current = visualNumber(streaks, "currentStreak")
	}
	plural := func(n float64) string {
		s := "days"
		if n == 1 {
			s = "day"
		}
		return fmt.Sprintf("%g %s", n, s)
	}
	row("Current streak", plural(current), "Longest streak", plural(visualNumber(streaks, "longestStreak")))
	days := visualNumber(stats, "totalDays")
	if !screenshot {
		if p.Range == "7d" {
			days = 7
		}
		if p.Range == "30d" {
			days = 30
		}
	}
	peak := "N/A"
	if raw := stats.Get("peakActivityHour"); raw != nil && string(raw) != "null" {
		n := visualNumber(stats, "peakActivityHour")
		peak = fmt.Sprintf("%g:00-%g:00", n, n+1)
	}
	row("Active days", fmt.Sprintf("%g/%g", visualNumber(stats, "activeDays"), days), "Peak hour", peak)
	if screenshot {
		lines = append(lines, "", visualHighlight(statsFunFactoid(stats, total)), fmt.Sprintf("\x1b[90mStats from the last %g days\x1b[0m", visualNumber(stats, "totalDays")))
	}
	if date, err := time.Parse("2006-01-02", stats.String("peakActivityDay")); err == nil && !screenshot {
		lines = append(lines, "Most active day: "+date.Format("Jan 2"))
	}
	return lines
}

func statsThemeColors(theme string) []string {
	switch theme {
	case "light":
		return []string{"\x1b[38;2;87;105;247m", "\x1b[38;2;44;122;57m", "\x1b[38;2;150;108;30m"}
	case "light-daltonized":
		return []string{"\x1b[38;2;51;102;255m", "\x1b[38;2;0;102;153m", "\x1b[38;2;255;153;0m"}
	case "dark-daltonized":
		return []string{"\x1b[38;2;153;204;255m", "\x1b[38;2;51;153;255m", "\x1b[38;2;255;204;0m"}
	case "light-ansi":
		return []string{"\x1b[34m", "\x1b[32m", "\x1b[33m"}
	case "dark-ansi":
		return []string{"\x1b[94m", "\x1b[92m", "\x1b[93m"}
	default:
		return []string{"\x1b[38;2;177;185;249m", "\x1b[38;2;78;186;101m", "\x1b[38;2;255;193;7m"}
	}
}
func statsFunFactoid(stats protocol.JSONObject, total float64) string {
	type comparison struct {
		name string
		n    float64
	}
	books := []comparison{{"The Little Prince", 22000}, {"The Old Man and the Sea", 35000}, {"A Christmas Carol", 37000}, {"Animal Farm", 39000}, {"Fahrenheit 451", 60000}, {"The Great Gatsby", 62000}, {"Slaughterhouse-Five", 64000}, {"Brave New World", 83000}, {"The Catcher in the Rye", 95000}, {"Harry Potter and the Philosopher's Stone", 103000}, {"The Hobbit", 123000}, {"1984", 123000}, {"To Kill a Mockingbird", 130000}, {"Pride and Prejudice", 156000}, {"Dune", 244000}, {"Moby-Dick", 268000}, {"Crime and Punishment", 274000}, {"A Game of Thrones", 381000}, {"Anna Karenina", 468000}, {"Don Quixote", 520000}, {"The Lord of the Rings", 576000}, {"The Count of Monte Cristo", 603000}, {"Les Misérables", 689000}, {"War and Peace", 730000}}
	choices := []string{}
	for _, book := range books {
		if total >= book.n {
			if ratio := total / book.n; ratio >= 2 {
				choices = append(choices, fmt.Sprintf("You've used ~%.0fx more tokens than %s", math.Floor(ratio), book.name))
			} else {
				choices = append(choices, "You've used the same number of tokens as "+book.name)
			}
		}
	}
	if longest, ok := stats.Object("longestSession"); ok {
		minutes := visualNumber(longest, "duration") / 60000
		for _, item := range []comparison{{"a TED talk", 18}, {"an episode of The Office", 22}, {"listening to Abbey Road", 47}, {"a yoga class", 60}, {"a World Cup soccer match", 90}, {"a half marathon (average time)", 120}, {"the movie Inception", 148}, {"watching Titanic", 195}, {"a transatlantic flight", 420}, {"a full night of sleep", 480}} {
			if ratio := minutes / item.n; ratio >= 2 {
				choices = append(choices, fmt.Sprintf("Your longest session is ~%.0fx longer than %s", math.Floor(ratio), item.name))
			}
		}
	}
	if len(choices) == 0 {
		return ""
	}
	return choices[rand.Intn(len(choices))]
}
