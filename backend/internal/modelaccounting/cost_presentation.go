package modelaccounting

import (
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf16"
)

// CostPresentation contains actual process clock and tool-counter observations.
// It never accepts cost, usage, model metadata, or precomputed command output.
type CostPresentation struct {
	NowMillis                func() float64
	StartMillis              float64
	LinesAdded, LinesRemoved int
	Dim                      bool
	CanonicalName            func(string) (string, error)
}

func durationText(ms float64) string {
	if ms < 60000 {
		if ms == 0 {
			return "0s"
		}
		if ms < 1 {
			return fmt.Sprintf("%.1fs", ms/1000)
		}
		return fmt.Sprintf("%.0fs", math.Floor(ms/1000))
	}
	days := int(math.Floor(ms / 86400000))
	hours := int(math.Floor(math.Mod(ms, 86400000) / 3600000))
	minutes := int(math.Floor(math.Mod(ms, 3600000) / 60000))
	seconds := int(math.Floor(math.Mod(ms, 60000)/1000 + 0.5))
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
func costText(v float64) string {
	if v > 0.5 {
		return fmt.Sprintf("$%.2f", math.Floor(v*100+0.5)/100)
	}
	return "$" + positiveFixed(v, 4)
}

// ECMAScript toFixed chooses the larger integer when two positive scaled
// integers are equally close. strconv/fmt uses ties-to-even instead.
func positiveFixed(v float64, digits int) string {
	rational := new(big.Rat).SetFloat64(v)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	numerator := new(big.Int).Mul(rational.Num(), scale)
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(numerator, rational.Denom(), rem)
	if new(big.Int).Lsh(rem, 1).Cmp(rational.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	text := q.String()
	if len(text) <= digits {
		text = strings.Repeat("0", digits+1-len(text)) + text
	}
	return text[:len(text)-digits] + "." + text[len(text)-digits:]
}

func numberText(v float64) (string, error) {
	if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return "", unsupported("cost format numeric domain")
	}
	// Intl compact en-US has four suffix scales; larger/scientific domains remain
	// explicit capabilities rather than approximate localized output.
	if v >= 1e15 {
		return "", unsupported("cost Intl compact beyond trillion")
	}
	suffix := ""
	scale := 1.0
	for _, p := range []struct {
		n float64
		s string
	}{{1e12, "t"}, {1e9, "b"}, {1e6, "m"}, {1e3, "k"}} {
		if v >= p.n {
			scale = p.n
			suffix = p.s
			break
		}
	}
	if math.Trunc(v) != v {
		return "", unsupported("cost Intl fractional token counters")
	}
	rounded := math.Floor(v/scale*10+0.5) / 10
	if rounded >= 1000 && suffix != "" && suffix != "t" {
		scale *= 1000
		switch suffix {
		case "k":
			suffix = "m"
		case "m":
			suffix = "b"
		case "b":
			suffix = "t"
		}
		rounded = math.Floor(v/scale*10+0.5) / 10
	}
	if suffix != "" {
		return strconv.FormatFloat(rounded, 'f', 1, 64) + suffix, nil
	}
	return strconv.FormatFloat(rounded, 'f', -1, 64), nil
}
func lineWord(n int) string {
	if n == 1 {
		return "line"
	}
	return "lines"
}
func (s *Session) FormatTotalCost(scope *cooperative.Scope, p CostPresentation) (string, error) {
	if s == nil || !scope.BelongsTo(s.owner) {
		return "", cooperative.ErrRestricted
	}
	if p.NowMillis == nil || p.LinesAdded < 0 || p.LinesRemoved < 0 {
		return "", unsupported("original cost clock/line counters")
	}
	start := p.StartMillis
	if s.restoredStartMillis != nil {
		start = *s.restoredStartMillis
	}
	wall := p.NowMillis() - start
	p.LinesAdded += s.restoredLinesAdded
	p.LinesRemoved += s.restoredLinesRemoved
	if math.IsNaN(wall) || math.IsInf(wall, 0) || wall < 0 {
		return "", unsupported("original cost wall clock")
	}
	if math.IsNaN(s.cost) || math.IsInf(s.cost, 0) || s.cost < 0 || s.cost >= 1e21 {
		return "", unsupported("cost display finite decimal domain")
	}
	cost := costText(s.cost)
	if s.unknownModelCost {
		cost += " (costs may be inaccurate due to usage of unknown models)"
	}
	usage := "Usage:                 0 input, 0 output, 0 cache read, 0 cache write"
	models := s.models
	if p.CanonicalName != nil {
		models = protocol.NewJSONObject()
		for _, property := range s.models.OwnProperties() {
			original := object([]byte(`{"x":` + string(property.NameJSON) + `}`)).String("x")
			name, e := p.CanonicalName(original)
			if e != nil {
				return "", e
			}
			previous := object(models.Get(name))
			incoming := object(property.Value)
			for _, key := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens", "webSearchRequests", "costUSD"} {
				if incoming.Get(key) == nil {
					return "", unsupported("stored cost display missing usage field " + key)
				}
				previous.Set(key, raw(num(previous.Get(key))+num(incoming.Get(key))))
			}
			models.Set(name, raw(previous))
		}
	}
	if len(models.OwnProperties()) > 0 {
		usage = "Usage by model:"
		for _, property := range models.OwnProperties() {
			model := object(property.Value)
			// NewCalculator admits unknown names without canonical aliases/overrides.
			name := object([]byte(`{"x":` + string(property.NameJSON) + `}`)).String("x")
			values := []string{}
			for _, key := range []string{"inputTokens", "outputTokens", "cacheReadInputTokens", "cacheCreationInputTokens"} {
				v, e := numberText(num(model.Get(key)))
				if e != nil {
					return "", e
				}
				values = append(values, v)
			}
			row := fmt.Sprintf("  %s input, %s output, %s cache read, %s cache write", values[0], values[1], values[2], values[3])
			if n := num(model.Get("webSearchRequests")); n > 0 {
				v, e := numberText(n)
				if e != nil {
					return "", e
				}
				row += ", " + v + " web search"
			}
			width := len(utf16.Encode([]rune(name))) + 1
			pad := 21 - width
			if pad < 0 {
				pad = 0
			}
			usage += "\n" + strings.Repeat(" ", pad) + name + ":" + row + " (" + costText(num(model.Get("costUSD"))) + ")"
		}
	}
	out := fmt.Sprintf("Total cost:            %s\nTotal duration (API):  %s\nTotal duration (wall): %s\nTotal code changes:    %d %s added, %d %s removed\n%s", cost, durationText(s.apiDuration), durationText(wall), p.LinesAdded, lineWord(p.LinesAdded), p.LinesRemoved, lineWord(p.LinesRemoved), usage)
	if p.Dim {
		out = "\x1b[2m" + out + "\x1b[22m"
	}
	return out, nil
}
