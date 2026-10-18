package tui

import (
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

// StatusLine's original Ansi consumes text and link actions. Device controls
// never become terminal commands; only the resulting text styles are emitted.
func TerminalStatusLineTextRows(text string, width, padding int, hyperlinks ...bool) []string {
	return TerminalStatusLineTextRowsForTheme(text, width, padding, "dark", len(hyperlinks) > 0 && hyperlinks[0])
}

func TerminalStatusLineTextRowsForTheme(text string, width, padding int, theme string, hyperlinks bool) []string {
	return TerminalStatusLineTextRowsForThemeNumberPadding(text, width, float64(padding), theme, hyperlinks)
}

// Source accepts a numeric padding. Yoga retains the fractional layout width
// while the output canvas floors its origin and clips outside the terminal.
func TerminalStatusLineTextRowsForThemeNumberPadding(text string, width int, padding float64, theme string, hyperlinks bool) []string {
	// Ink's root chooses 80 columns when its output has no usable width.
	if width == 0 {
		width = 80
	}
	available := max(0, int(math.Floor(float64(width)-2*padding)))
	origin := int(math.Floor(padding))
	if available == 0 {
		return []string{"", ""}
	}
	base := strings.TrimSuffix(strings.TrimPrefix(TerminalThemeRoleEscape(theme, "inactive", false), "\x1b["), "m")
	cells := terminalStatusLineParse(text, hyperlinks, base)
	// Yoga measures the truncated text first. Rendering uses that measured
	// width, and only wraps when the original widest line exceeds it.
	measured := terminalStatusLineTruncate(cells, available)
	actualWidth := min(available, terminalStatusLineWidest(measured))
	if terminalStatusLineWidest(cells) > actualWidth {
		cells = terminalStatusLineTruncate(cells, actualWidth)
	}
	rows := []string{strings.Repeat(" ", max(0, min(origin, width)))}
	column := origin
	for _, cell := range cells {
		if cell.text == "\n" {
			rows = append(rows, strings.Repeat(" ", max(0, min(origin, width))))
			column = origin
			continue
		}
		if cell.text == "\t" {
			n := min(8-column%8, max(0, width-column))
			rows[len(rows)-1] += strings.Repeat(" ", n)
			column += n
			continue
		}
		n := uniseg.StringWidth(cell.text)
		if column < 0 || column+n > width {
			column += n
			continue
		}
		span := "\x1b[0;" + cell.style.sgr() + "m" + cell.text + "\x1b[0m"
		if cell.style.link != "" {
			span = "\x1b]8;;" + cell.style.link + "\x1b\\" + span + "\x1b]8;;\x1b\\"
		}
		rows[len(rows)-1] += span
		column += n
	}
	return rows
}

type terminalStatusLineStyle struct {
	fg, bg, link                                  string
	bold, dim, italic, underline, inverse, strike bool
}
type terminalStatusLineCell struct {
	text  string
	style terminalStatusLineStyle
}

func (s terminalStatusLineStyle) sgr() string {
	values := []string{s.fg}
	if s.bg != "" {
		values = append(values, s.bg)
	}
	// The original StyledText gives dim precedence when both weights are set.
	if s.dim {
		values = append(values, "2")
	} else if s.bold {
		values = append(values, "1")
	}
	for _, v := range []struct {
		enabled bool
		code    string
	}{{s.italic, "3"}, {s.underline, "4"}, {s.inverse, "7"}, {s.strike, "9"}} {
		if v.enabled {
			values = append(values, v.code)
		}
	}
	return strings.Join(values, ";")
}
func terminalStatusLineWidest(cells []terminalStatusLineCell) int {
	width, maxWidth := 0, 0
	for _, c := range cells {
		if c.text == "\n" {
			maxWidth = max(maxWidth, width)
			width = 0
		} else {
			width += uniseg.StringWidth(c.text)
		}
	}
	return max(maxWidth, width)
}
func terminalStatusLineTruncate(cells []terminalStatusLineCell, width int) []terminalStatusLineCell {
	if width < 1 {
		return nil
	}
	if width == 1 {
		if len(cells) == 0 {
			return nil
		}
		return []terminalStatusLineCell{{text: "…", style: cells[0].style}}
	}
	total := 0
	for _, c := range cells {
		total += uniseg.StringWidth(c.text)
	}
	if total <= width {
		return cells
	}
	limit := width - 1
	// Source sliceFit retries the boundary once when a wide glyph overshoots.
	cut := func(end int) ([]terminalStatusLineCell, int, int) {
		out := []terminalStatusLineCell{}
		position, index := 0, 0
		for i, c := range cells {
			n := uniseg.StringWidth(c.text)
			if position >= end && n > 0 {
				index = i
				return out, position, index
			}
			out = append(out, c)
			position += n
			index = i + 1
		}
		return out, position, index
	}
	out, length, index := cut(limit)
	if length > limit {
		out, _, index = cut(limit - 1)
	}
	style := terminalStatusLineStyle{}
	if index < len(cells) {
		style = cells[index].style
	} else if len(out) > 0 {
		style = out[len(out)-1].style
	}
	return append(out, terminalStatusLineCell{text: "…", style: style})
}

func terminalStatusLineParse(text string, links bool, base string) []terminalStatusLineCell {
	cells := []terminalStatusLineCell{}
	style := terminalStatusLineStyle{fg: base}
	emit := func(value string) {
		g := uniseg.NewGraphemes(value)
		for g.Next() {
			s := g.Str()
			if s == "\r\n" {
				s = "\n"
			}
			s = strings.Map(func(r rune) rune {
				if r == '\n' || r == '\t' || !unicode.IsControl(r) {
					return r
				}
				return -1
			}, s)
			if s != "" {
				cells = append(cells, terminalStatusLineCell{text: s, style: style})
			}
		}
	}
	for i := 0; i < len(text); {
		if text[i] != 0x1b {
			start := i
			for i < len(text) && text[i] != 0x1b {
				i++
			}
			emit(text[start:i])
			continue
		}
		i++
		if i >= len(text) {
			break
		}
		switch text[i] {
		case '[':
			i++
			start := i
			for i < len(text) && !(text[i] >= 0x40 && text[i] <= 0x7e) {
				i++
			}
			if i < len(text) {
				if text[i] == 'm' {
					terminalStatusLineApplySGR(&style, text[start:i], base)
				}
				i++
			}
		case ']', 'P', '_':
			kind := text[i]
			i++
			start := i
			for i < len(text) && text[i] != 7 && !(text[i] == 0x1b && i+1 < len(text) && text[i+1] == '\\') {
				i++
			}
			if i >= len(text) {
				break
			}
			if kind == ']' && links && strings.HasPrefix(text[start:i], "8;") {
				parts := strings.SplitN(text[start:i], ";", 3)
				if len(parts) == 3 && strings.IndexFunc(parts[2], unicode.IsControl) < 0 {
					style.link = parts[2]
				}
			}
			if text[i] == 7 {
				i++
			} else {
				i += 2
			}
		default:
			// ESC intermediates consume the final designator too (e.g. ESC ( 0).
			for i < len(text) && text[i] >= 0x20 && text[i] <= 0x2f {
				i++
			}
			if i < len(text) {
				i++
			}
		}
	}
	return cells
}

type terminalStatusLineSGRParam struct {
	code  int
	sub   []int
	colon bool
}

func terminalStatusLineApplySGR(s *terminalStatusLineStyle, parameters, base string) {
	params := []terminalStatusLineSGRParam{}
	for _, part := range strings.Split(parameters, ";") {
		pieces := strings.Split(part, ":")
		n, _ := strconv.Atoi(pieces[0])
		p := terminalStatusLineSGRParam{code: n, colon: len(pieces) > 1}
		for _, sub := range pieces[1:] {
			if sub != "" {
				value, _ := strconv.Atoi(sub)
				p.sub = append(p.sub, value)
			}
		}
		params = append(params, p)
	}
	color := func(i int) (string, int) {
		p := params[i]
		values := p.sub
		if !p.colon {
			values = nil
			for _, v := range params[i+1:] {
				values = append(values, v.code)
			}
		}
		count := 0
		prefix := ""
		if len(values) >= 2 && values[0] == 5 {
			count = 3
			prefix = "5;" + strconv.Itoa(values[1])
		} else if len(values) >= 4 && values[0] == 2 {
			offset := 1
			if p.colon && len(values) >= 5 {
				offset = 2
			}
			count = 5
			prefix = "2;" + strconv.Itoa(values[offset]) + ";" + strconv.Itoa(values[offset+1]) + ";" + strconv.Itoa(values[offset+2])
		}
		if p.colon && count > 0 {
			count = 1
		}
		return prefix, count
	}
	for i := 0; i < len(params); i++ {
		p := params[i]
		n := p.code
		switch {
		case n == 0:
			link := s.link
			*s = terminalStatusLineStyle{fg: base, link: link}
		case n == 1:
			s.bold = true
		case n == 2:
			s.dim = true
		case n == 3:
			s.italic = true
		case n == 4:
			s.underline = !p.colon || len(p.sub) == 0 || p.sub[0] != 0
		case n == 21:
			s.underline = true
		case n == 22:
			s.bold = false
			s.dim = false
		case n == 23:
			s.italic = false
		case n == 24:
			s.underline = false
		case n == 7:
			s.inverse = true
		case n == 27:
			s.inverse = false
		case n == 9:
			s.strike = true
		case n == 29:
			s.strike = false
		case n == 39:
			s.fg = base
		case n == 49:
			s.bg = ""
		case n >= 30 && n <= 37 || n >= 90 && n <= 97:
			s.fg = strconv.Itoa(n)
		case n >= 40 && n <= 47 || n >= 100 && n <= 107:
			s.bg = strconv.Itoa(n)
		case n == 38 || n == 48 || n == 58:
			value, count := color(i)
			if count > 0 {
				if n == 38 {
					s.fg = "38;" + value
				} else if n == 48 {
					s.bg = "48;" + value
				}
				i += count - 1
			}
		}
	}
}
