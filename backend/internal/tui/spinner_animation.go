package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/rivo/uniseg"
)

// The shared animation clock is independent of normal terminal refreshes.
// Merely reading View or changing a response-length ref does not tick it.
type terminalSpinnerAnimationMsg struct {
	now       time.Time
	clockTime time.Duration
	revision  uint64
}

// SpinnerAnimationRow owns these refs for one mounted child. render is called
// on a committed parent change or its shared-clock callback, never on View.
type terminalSpinnerAnimationState struct {
	initialized       bool
	displayedResponse int
	lastResponse      int
	lastTokenTime     float64
	mountTime         float64
	lastSmoothTime    float64
	stalledIntensity  float64
	isStalled         bool
	clockTime         float64
}

func (r *terminalSpinnerAnimationState) render(response int, clockTime time.Duration, reduced, activeTools bool) {
	t := float64(clockTime) / float64(time.Millisecond)
	if !r.initialized {
		r.initialized = true
		r.displayedResponse, r.lastResponse = response, response
		r.lastTokenTime, r.mountTime, r.lastSmoothTime = t, t, t
	}
	if response > r.lastResponse {
		r.lastTokenTime, r.lastResponse = t, response
		r.stalledIntensity, r.lastSmoothTime = 0, t
	}
	timeSinceToken := t - r.mountTime
	if activeTools {
		timeSinceToken, r.lastTokenTime = 0, t
	} else if response > 0 {
		timeSinceToken = t - r.lastTokenTime
	}
	r.isStalled = timeSinceToken > 3000 && !activeTools
	intensity := float64(0)
	if r.isStalled {
		intensity = math.Min((timeSinceToken-3000)/2000, 1)
	}
	if !reduced && (intensity > 0 || r.stalledIntensity > 0) {
		dt := t - r.lastSmoothTime
		if dt >= 50 {
			for i := 0; i < int(math.Floor(dt/50)); i++ {
				difference := intensity - r.stalledIntensity
				if math.Abs(difference) < .01 {
					r.stalledIntensity = intensity
					break
				}
				r.stalledIntensity += difference * .1
			}
			r.lastSmoothTime = t
		}
	} else {
		r.stalledIntensity, r.lastSmoothTime = intensity, t
	}
	if reduced {
		r.stalledIntensity, r.displayedResponse = intensity, response
	} else if gap := response - r.displayedResponse; gap > 0 {
		increment := 50
		if gap < 70 {
			increment = 3
		} else if gap < 200 {
			increment = max(8, int(math.Ceil(float64(gap)*.15)))
		}
		r.displayedResponse = min(r.displayedResponse+increment, response)
	}
	r.clockTime = t
}

var terminalSpinnerExtraColors = map[string]map[string]string{
	"dark":             {"claudeShimmer": "rgb(235,159,127)", "claudeBlue_FOR_SYSTEM_SPINNER": "rgb(147,165,255)", "claudeBlueShimmer_FOR_SYSTEM_SPINNER": "rgb(177,195,255)"},
	"light":            {"claudeShimmer": "rgb(245,149,117)", "claudeBlue_FOR_SYSTEM_SPINNER": "rgb(87,105,247)", "claudeBlueShimmer_FOR_SYSTEM_SPINNER": "rgb(117,135,255)"},
	"dark-daltonized":  {"claudeShimmer": "rgb(255,183,101)", "claudeBlue_FOR_SYSTEM_SPINNER": "rgb(153,204,255)", "claudeBlueShimmer_FOR_SYSTEM_SPINNER": "rgb(183,224,255)"},
	"light-daltonized": {"claudeShimmer": "rgb(255,183,101)", "claudeBlue_FOR_SYSTEM_SPINNER": "rgb(51,102,255)", "claudeBlueShimmer_FOR_SYSTEM_SPINNER": "rgb(101,152,255)"},
	"dark-ansi":        {"claudeShimmer": "ansi:yellowBright", "claudeBlue_FOR_SYSTEM_SPINNER": "ansi:blueBright", "claudeBlueShimmer_FOR_SYSTEM_SPINNER": "ansi:blueBright"},
	"light-ansi":       {"claudeShimmer": "ansi:yellowBright", "claudeBlue_FOR_SYSTEM_SPINNER": "ansi:blue", "claudeBlueShimmer_FOR_SYSTEM_SPINNER": "ansi:blueBright"},
}

func terminalSpinnerColor(theme, role string) string {
	values, ok := terminalSpinnerExtraColors[theme]
	if !ok {
		values = terminalSpinnerExtraColors["dark"]
	}
	if value := values[role]; value != "" {
		return value
	}
	return TerminalThemeRole(theme, role)
}

func terminalSpinnerRGB(value string) ([3]int, bool) {
	var rgb [3]int
	n, err := fmt.Sscanf(value, "rgb(%d,%d,%d)", &rgb[0], &rgb[1], &rgb[2])
	return rgb, err == nil && n == 3
}

func terminalSpinnerInterpolate(a, b [3]int, opacity float64) string {
	rgb := [3]int{}
	for i := range rgb {
		rgb[i] = int(math.Floor(float64(a[i]) + float64(b[i]-a[i])*opacity + .5))
	}
	return fmt.Sprintf("rgb(%d,%d,%d)", rgb[0], rgb[1], rgb[2])
}

func terminalSpinnerColored(value, color string) string {
	return terminalThemeColorEscape(color, false) + displayText(value) + "\x1b[0m"
}

func terminalSpinnerStallColor(theme, role string, intensity float64) string {
	base := terminalSpinnerColor(theme, role)
	if intensity > 0 {
		if rgb, ok := terminalSpinnerRGB(base); ok {
			return terminalSpinnerInterpolate(rgb, [3]int{171, 43, 63}, intensity)
		}
		if intensity > .5 {
			return TerminalThemeRole(theme, "error")
		}
	}
	return base
}

func (r *terminalSpinnerAnimationState) glyph(characters []string, theme, role string, reduced, override bool) string {
	if reduced {
		if int(math.Floor(r.clockTime/1000))%2 == 1 {
			role = "inactive"
		}
		return terminalSpinnerColored("●", terminalSpinnerColor(theme, role))
	}
	intensity := r.stalledIntensity
	if override {
		intensity = 0
	}
	frame := int(math.Floor(r.clockTime/120)) % (len(characters) * 2)
	if frame >= len(characters) {
		frame = len(characters)*2 - frame - 1
	}
	return terminalSpinnerColored(characters[frame], terminalSpinnerStallColor(theme, role, intensity))
}

func (r *terminalSpinnerAnimationState) messageParts(message, mode, theme, role, shimmer string, reduced, override bool) []terminalSpinnerFlexPart {
	base, highlight := terminalSpinnerColor(theme, role), terminalSpinnerColor(theme, shimmer)
	paint := func(value, color string) terminalSpinnerFlexPart {
		return terminalSpinnerFlexPart{value: terminalSpinnerColored(value, color)}
	}
	plain := func(color string) []terminalSpinnerFlexPart {
		return []terminalSpinnerFlexPart{paint(message, color), paint(" ", base)}
	}
	intensity := r.stalledIntensity
	if override {
		intensity = 0
	}
	if intensity > 0 {
		color := terminalSpinnerStallColor(theme, role, intensity)
		return []terminalSpinnerFlexPart{paint(message, color), paint(" ", color)}
	}
	if mode == "tool-use" {
		opacity := float64(0)
		if !reduced {
			opacity = (math.Sin(r.clockTime/1000*math.Pi) + 1) / 2
		}
		if a, ok := terminalSpinnerRGB(base); ok {
			if b, ok := terminalSpinnerRGB(highlight); ok {
				return plain(terminalSpinnerInterpolate(a, b, opacity))
			}
		}
		if opacity > .5 {
			return plain(highlight)
		}
		return plain(base)
	}
	width := exportWidth(message)
	index := -100
	if !reduced && !r.isStalled {
		speed := float64(200)
		if mode == "requesting" {
			speed = 50
		}
		position := int(math.Floor(r.clockTime/speed)) % (width + 20)
		index = width + 10 - position
		if mode == "requesting" {
			index = position - 10
		}
	}
	start, end := index-1, index+1
	if start >= width || end < 0 {
		return plain(base)
	}
	start = max(0, start)
	var before, middle, after strings.Builder
	graphemes, column := uniseg.NewGraphemes(message), 0
	for graphemes.Next() {
		segment := graphemes.Str()
		w := exportWidth(segment)
		if column+w <= start {
			before.WriteString(segment)
		} else if column > end {
			after.WriteString(segment)
		} else {
			middle.WriteString(segment)
		}
		column += w
	}
	parts := []terminalSpinnerFlexPart{}
	if before.Len() > 0 {
		parts = append(parts, paint(before.String(), base))
	}
	parts = append(parts, paint(middle.String(), highlight))
	if after.Len() > 0 {
		parts = append(parts, paint(after.String(), base))
	}
	return append(parts, paint(" ", base))
}

func (r *terminalSpinnerAnimationState) thinkingColor() string {
	opacity := float64(0)
	if r.clockTime >= 3000 {
		opacity = (math.Sin((r.clockTime-3000)/1000*math.Pi) + 1) / 2
	}
	return terminalSpinnerInterpolate([3]int{153, 153, 153}, [3]int{185, 185, 185}, opacity)
}
