package terminalcapabilities

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ColorSupport struct {
	Level     float64
	Supported bool
}

var color256Term = regexp.MustCompile(`(?i)-256(color)?$`)
var colorBasicTerm = regexp.MustCompile(`(?i)^screen|^xterm|^vt100|^vt220|^rxvt|color|ansi|cygwin|linux`)
var colorTeamCity = regexp.MustCompile(`^(9\.(0*[1-9]\d*)\.|\d{2,}\.)`)

func decimalPrefix(value string) float64 {
	value = protocol.TrimJavaScriptString(value)
	end := 0
	if len(value) > 0 && (value[0] == '+' || value[0] == '-') {
		end++
	}
	start := end
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if start == end {
		return math.NaN()
	}
	number, _ := strconv.ParseFloat(value[:end], 64)
	return number
}

// ColorLibrary preserves both actual dependency versions: Chalk's vendored
// detector keeps invalid FORCE_COLOR values, while supports-hyperlinks uses
// the newer detector which rejects values outside 0..3.
func ColorLibrary(env map[string]string, platform, release string, tty bool, args []string, modern bool) ColorSupport {
	flags := map[string]bool{}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		flags[arg] = true
	}
	force := math.NaN()
	forced := false
	if flags["--no-color"] || flags["--no-colors"] || flags["--color=false"] || flags["--color=never"] {
		force, forced = 0, true
	} else if flags["--color"] || flags["--colors"] || flags["--color=true"] || flags["--color=always"] {
		force, forced = 1, true
	}
	if value, exists := env["FORCE_COLOR"]; exists {
		level := math.Min(decimalPrefix(value), 3)
		switch value {
		case "", "true":
			level = 1
		case "false":
			level = 0
		}
		if !modern || level == 0 || level == 1 || level == 2 || level == 3 {
			force, forced = level, true
		}
	}
	result := func(level float64) ColorSupport { return ColorSupport{level, level != 0} }
	if forced && force == 0 {
		return result(0)
	}
	if flags["--color=16m"] || flags["--color=full"] || flags["--color=truecolor"] {
		return result(3)
	}
	if flags["--color=256"] {
		return result(2)
	}
	if _, build := env["TF_BUILD"]; build {
		if _, agent := env["AGENT_NAME"]; agent {
			return result(1)
		}
	}
	if !tty && !forced {
		return result(0)
	}
	minimum := 0.0
	if forced && !math.IsNaN(force) {
		minimum = force
	}
	if env["TERM"] == "dumb" {
		return result(minimum)
	}
	if platform == "windows" || platform == "win32" {
		version := strings.Split(release, ".")
		if len(version) > 2 {
			major, _ := strconv.ParseFloat(version[0], 64)
			build, _ := strconv.ParseFloat(version[2], 64)
			if major >= 10 && build >= 10586 {
				if build >= 14931 {
					return result(3)
				}
				return result(2)
			}
		}
		return result(1)
	}
	if _, ci := env["CI"]; ci {
		for _, key := range []string{"GITHUB_ACTIONS", "GITEA_ACTIONS", "CIRCLECI"} {
			if _, exists := env[key]; exists {
				return result(3)
			}
		}
		for _, key := range []string{"TRAVIS", "APPVEYOR", "GITLAB_CI", "BUILDKITE", "DRONE"} {
			if _, exists := env[key]; exists {
				return result(1)
			}
		}
		if env["CI_NAME"] == "codeship" {
			return result(1)
		}
		return result(minimum)
	}
	if teamcity, exists := env["TEAMCITY_VERSION"]; exists {
		if colorTeamCity.MatchString(teamcity) {
			return result(1)
		}
		return result(0)
	}
	if env["COLORTERM"] == "truecolor" || env["TERM"] == "xterm-kitty" || env["TERM"] == "xterm-ghostty" || env["TERM"] == "wezterm" {
		return result(3)
	}
	switch env["TERM_PROGRAM"] {
	case "iTerm.app":
		if decimalPrefix(strings.Split(env["TERM_PROGRAM_VERSION"], ".")[0]) >= 3 {
			return result(3)
		}
		return result(2)
	case "Apple_Terminal":
		return result(2)
	}
	if color256Term.MatchString(env["TERM"]) {
		return result(2)
	}
	if colorBasicTerm.MatchString(env["TERM"]) {
		return result(1)
	}
	if _, exists := env["COLORTERM"]; exists {
		return result(1)
	}
	return result(minimum)
}

func InkColorLevel(env map[string]string, level float64) float64 {
	if env["TERM_PROGRAM"] == "vscode" && level == 2 {
		level = 3
	}
	if env["CLAUDE_CODE_TMUX_TRUECOLOR"] == "" && env["TMUX"] != "" && level > 2 {
		level = 2
	}
	return level
}

// WrapColor includes Chalk's closing-code replacement and line-break framing.
func WrapColor(text, open, close string, level float64) string {
	if level <= 0 || text == "" {
		return text
	}
	text = strings.ReplaceAll(text, close, close+open)
	var framed strings.Builder
	framed.WriteString(open)
	start := 0
	for {
		index := strings.IndexByte(text[start:], '\n')
		if index < 0 {
			break
		}
		index += start
		end, line := index, "\n"
		if index > start && text[index-1] == '\r' {
			end, line = index-1, "\r\n"
		}
		framed.WriteString(text[start:end])
		framed.WriteString(close)
		framed.WriteString(line)
		framed.WriteString(open)
		start = index + 1
	}
	framed.WriteString(text[start:])
	framed.WriteString(close)
	return framed.String()
}

func RGBColorEscape(red, green, blue int, level float64) string {
	if level <= 0 {
		return ""
	}
	if level == 3 {
		return "\x1b[38;2;" + strconv.Itoa(red) + ";" + strconv.Itoa(green) + ";" + strconv.Itoa(blue) + "m"
	}
	code := 16 + 36*int(math.Floor(float64(red)/255*5+0.5)) + 6*int(math.Floor(float64(green)/255*5+0.5)) + int(math.Floor(float64(blue)/255*5+0.5))
	if red == green && green == blue {
		if red < 8 {
			code = 16
		} else if red > 248 {
			code = 231
		} else {
			code = int(math.Floor(float64(red-8)/247*24+0.5)) + 232
		}
	}
	if level == 2 {
		return "\x1b[38;5;" + strconv.Itoa(code) + "m"
	}
	ansi := 0
	if code < 8 {
		ansi = 30 + code
	} else if code < 16 {
		ansi = 90 + code - 8
	} else {
		var r, g, b float64
		if code >= 232 {
			r = float64((code-232)*10+8) / 255
			g, b = r, r
		} else {
			code -= 16
			r = math.Floor(float64(code)/36) / 5
			g = math.Floor(float64(code%36)/6) / 5
			b = float64(code%6) / 5
		}
		value := math.Max(r, math.Max(g, b)) * 2
		ansi = 30
		if value != 0 {
			ansi += int(math.Floor(b+0.5))<<2 | int(math.Floor(g+0.5))<<1 | int(math.Floor(r+0.5))
			if value == 2 {
				ansi += 60
			}
		}
	}
	return "\x1b[" + strconv.Itoa(ansi) + "m"
}
