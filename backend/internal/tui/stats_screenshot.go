package tui

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"fmt"
	"github.com/rivo/uniseg"
	"image"
	"image/png"
	"math"
	"strconv"
	"strings"
)

// Source Fira Code 24×48 alpha masks, SIL OFL 1.1. The bundled asset is the
// exact existing Source FONT_B64 decoded without rerasterization/system fonts.
//
//go:embed stats_screenshot_font.bin
var statsScreenshotFont []byte

type screenshotColor [3]float64
type screenshotGlyph struct {
	char  rune
	color screenshotColor
	bold  bool
}

var screenshotBasic = []screenshotColor{{0, 0, 0}, {205, 49, 49}, {13, 188, 121}, {229, 229, 16}, {36, 114, 200}, {188, 63, 188}, {17, 168, 205}, {229, 229, 229}, {102, 102, 102}, {241, 76, 76}, {35, 209, 139}, {245, 245, 67}, {59, 142, 234}, {214, 112, 214}, {41, 184, 219}, {255, 255, 255}}

func screenshot256(n int) screenshotColor {
	standard := []screenshotColor{{0, 0, 0}, {128, 0, 0}, {0, 128, 0}, {128, 128, 0}, {0, 0, 128}, {128, 0, 128}, {0, 128, 128}, {192, 192, 192}, {128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0}, {0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255}}
	if n >= 0 && n < 16 {
		return standard[n]
	}
	if n < 232 {
		n -= 16
		channel := func(v int) float64 {
			if v == 0 {
				return 0
			}
			return float64(55 + v*40)
		}
		return screenshotColor{channel(n / 36), channel(n % 36 / 6), channel(n % 6)}
	}
	g := float64((n-232)*10 + 8)
	return screenshotColor{g, g, g}
}
func screenshotLines(text string) [][]screenshotGlyph {
	rows := [][]screenshotGlyph{}
	for _, line := range strings.Split(text, "\n") {
		chars := []rune(line)
		color := screenshotColor{229, 229, 229}
		bold := false
		row := []screenshotGlyph{}
		for i := 0; i < len(chars); {
			if chars[i] == 27 && i+1 < len(chars) && chars[i+1] == '[' {
				j := i + 2
				for j < len(chars) && !(chars[j] >= 'a' && chars[j] <= 'z' || chars[j] >= 'A' && chars[j] <= 'Z') {
					j++
				}
				if j < len(chars) && chars[j] == 'm' {
					codes := []int{}
					for _, part := range strings.Split(string(chars[i+2:j]), ";") {
						n, _ := strconv.Atoi(part)
						codes = append(codes, n)
					}
					for k := 0; k < len(codes); k++ {
						n := codes[k]
						switch {
						case n == 0:
							color, bold = screenshotColor{229, 229, 229}, false
						case n == 1:
							bold = true
						case n >= 30 && n <= 37:
							color = screenshotBasic[n-30]
						case n >= 90 && n <= 97:
							color = screenshotBasic[n-90+8]
						case n == 39:
							color = screenshotColor{229, 229, 229}
						case n == 38 && k+2 < len(codes) && codes[k+1] == 5:
							color = screenshot256(codes[k+2])
							k += 2
						case n == 38 && k+4 < len(codes) && codes[k+1] == 2:
							color = screenshotColor{float64(codes[k+2]), float64(codes[k+3]), float64(codes[k+4])}
							k += 4
						}
					}
				}
				i = min(j+1, len(chars))
				continue
			}
			row = append(row, screenshotGlyph{chars[i], color, bold})
			i++
		}
		rows = append(rows, row)
	}
	for len(rows) > 0 {
		text := ""
		for _, g := range rows[len(rows)-1] {
			text += string(g.char)
		}
		if strings.TrimSpace(text) != "" {
			break
		}
		rows = rows[:len(rows)-1]
	}
	if len(rows) == 0 {
		rows = append(rows, nil)
	}
	return rows
}

// StatsScreenshotPNG uses the original bundled bitmap and alpha arithmetic.
// Its PNG compression may differ; the decoded RGBA pixels follow Source.
func StatsScreenshotPNG(ansiText string) ([]byte, error) {
	glyphs := map[rune][]byte{}
	font := statsScreenshotFont
	if len(font) < 2 {
		return nil, fmt.Errorf("missing Source screenshot font")
	}
	count := int(binary.LittleEndian.Uint16(font))
	offset := 2
	for i := 0; i < count; i++ {
		if offset+4+1152 > len(font) {
			return nil, fmt.Errorf("invalid Source screenshot font")
		}
		cp := rune(binary.LittleEndian.Uint32(font[offset:]))
		offset += 4
		glyphs[cp] = font[offset : offset+1152]
		offset += 1152
	}
	fallback := make([]byte, 1152)
	for y := 2; y < 44; y++ {
		for x := 1; x < 23; x++ {
			if (y == 2 || y == 43 || x == 1 || x == 22) && (x+y)%2 == 0 {
				fallback[y*24+x] = 255
			}
		}
	}
	rows := screenshotLines(ansiText)
	columns := 1
	for _, row := range rows {
		n := 0
		for _, g := range row {
			n += uniseg.StringWidth(string(g.char))
		}
		columns = max(columns, n)
	}
	width, height := columns*24+96, len(rows)*48+96
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 30, 30, 30, 255
	}
	for dy := 0; dy < 16; dy++ {
		for dx := 0; dx < 16; dx++ {
			ox, oy := 15.5-float64(dx), 15.5-float64(dy)
			if ox*ox+oy*oy <= 256 {
				continue
			}
			for _, point := range [][2]int{{dx, dy}, {width - 1 - dx, dy}, {dx, height - 1 - dy}, {width - 1 - dx, height - 1 - dy}} {
				img.Pix[point[1]*img.Stride+point[0]*4+3] = 0
			}
		}
	}
	shades := map[rune]float64{'░': .25, '▒': .5, '▓': .75, '█': 1}
	for r, row := range rows {
		column := 0
		for _, g := range row {
			cells := uniseg.StringWidth(string(g.char))
			if cells == 0 {
				continue
			}
			x, y := 48+column*24, 48+r*48
			glyph := glyphs[g.char]
			if glyph == nil {
				glyph = fallback
			}
			shade, isShade := shades[g.char]
			for gy := 0; gy < 48; gy++ {
				for gx := 0; gx < 24; gx++ {
					i := (y+gy)*img.Stride + (x+gx)*4
					if isShade {
						for c := 0; c < 3; c++ {
							img.Pix[i+c] = byte(math.Floor(g.color[c]*shade + 30*(1-shade) + .5))
						}
						continue
					}
					a := float64(glyph[gy*24+gx])
					if a == 0 {
						continue
					}
					if g.bold {
						a = math.Min(255, a*1.4)
					}
					for c := 0; c < 3; c++ {
						img.Pix[i+c] = byte(int(g.color[c]*a+float64(img.Pix[i+c])*(255-a)) >> 8)
					}
				}
			}
			column += cells
		}
	}
	var output bytes.Buffer
	err := png.Encode(&output, img)
	return output.Bytes(), err
}
