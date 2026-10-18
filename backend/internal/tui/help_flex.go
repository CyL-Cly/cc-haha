package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Help uses only the single-line stretch/default-shrink subset of the frozen
// Ink layout. Preserve its measure pass separately from its pixel-grid paint
// pass: narrow Text nodes can paint beyond a shrunken parent row.
type terminalHelpFlex struct {
	text                                     string
	key                                      string
	row                                      bool
	noShrink                                 bool
	children                                 []*terminalHelpFlex
	fixedWidth, gap, px, py, top, margin, pl float64
	w, h, x, y                               float64
	measures                                 map[terminalHelpMeasureKey][2]float64
	paintedX, paintedY                       int
}

type terminalHelpPaint struct {
	root  *terminalHelpFlex
	cells [][]*terminalStatusLineCell
}

type terminalHelpMeasureKey struct {
	width, height      uint64
	exactW, forceWidth bool
}

func helpText(value string) *terminalHelpFlex { return &terminalHelpFlex{text: value} }
func helpColumn(children ...*terminalHelpFlex) *terminalHelpFlex {
	return &terminalHelpFlex{children: children}
}
func helpRow(children ...*terminalHelpFlex) *terminalHelpFlex {
	return &terminalHelpFlex{row: true, children: children}
}

func helpTextMeasure(text string, width float64) (float64, float64) {
	plain := ansi.Strip(text)
	if plain == "" {
		return 0, 0
	}
	lines := strings.Split(plain, "\n")
	intrinsic := 0
	for _, line := range lines {
		intrinsic = max(intrinsic, exportWidth(line))
	}
	if !math.IsNaN(width) && width >= 1 && float64(intrinsic) > width {
		lines = exportWrapWords(plain, max(1, int(math.Floor(width))))
	}
	measured, height := 0, 0.0
	for _, line := range lines {
		cells := exportWidth(line)
		measured = max(measured, cells)
		if math.IsNaN(width) || width <= 0 || cells == 0 {
			height++
		} else {
			height += math.Ceil(float64(cells) / width)
		}
	}
	return float64(measured), height
}

func (n *terminalHelpFlex) layout(width, height float64, exactW, exactH, forceWidth bool) {
	key := terminalHelpMeasureKey{math.Float64bits(width), math.Float64bits(height), exactW, forceWidth}
	if !exactH {
		if size, ok := n.measures[key]; ok {
			n.w, n.h = size[0], size[1]
			return
		}
		defer func() {
			if n.measures == nil {
				n.measures = map[terminalHelpMeasureKey][2]float64{}
			}
			n.measures[key] = [2]float64{n.w, n.h}
		}()
	}
	if n.fixedWidth > 0 && !forceWidth {
		width = n.fixedWidth
		exactW = true
	}
	if n.text != "" {
		n.w, n.h = helpTextMeasure(n.text, width)
		if exactW {
			n.w = width
		}
		if exactH {
			n.h = height
		}
		return
	}
	innerW, innerH := width-2*n.px-n.pl, height-2*n.py-n.top
	if !math.IsNaN(innerW) {
		innerW = max(0, innerW)
	}
	if !math.IsNaN(innerH) {
		innerH = max(0, innerH)
	}
	bases := make([]float64, len(n.children))
	sum, margins := 0.0, 0.0
	for i, c := range n.children {
		if n.row {
			c.layout(innerW, innerH, false, exactH, false)
			bases[i] = c.w
		} else {
			c.layout(innerW, math.NaN(), exactW, false, false)
			bases[i] = c.h
			margins += c.margin
		}
		sum += bases[i]
	}
	space := innerH
	if n.row {
		space = innerW
	}
	space -= margins + n.gap*float64(max(0, len(n.children)-1))
	if !math.IsNaN(space) && sum > space && sum > 0 {
		shrinkable := 0.0
		for i, c := range n.children {
			if !c.noShrink {
				shrinkable += bases[i]
			}
		}
		for i := range bases {
			if !n.children[i].noShrink && shrinkable > 0 {
				bases[i] = max(0, bases[i]-(sum-space)*bases[i]/shrinkable)
			}
		}
	}
	cross, main := 0.0, 0.0
	for i, c := range n.children {
		if n.row {
			c.layout(bases[i], innerH, true, exactH, true)
			cross = max(cross, c.h)
			main += c.w
		} else {
			c.layout(innerW, bases[i], exactW, true, false)
			cross = max(cross, c.w)
			main += c.h + c.margin
		}
	}
	main += n.gap * float64(max(0, len(n.children)-1))
	if n.row {
		n.w = main + 2*n.px + n.pl
		n.h = cross + 2*n.py + n.top
	} else {
		n.w = cross + 2*n.px + n.pl
		n.h = main + 2*n.py + n.top
	}
	if exactW {
		n.w = width
	}
	if exactH {
		n.h = height
	}
	// Stretch into the measured cross size, as in the original second pass.
	if n.row && !exactH {
		for i, c := range n.children {
			c.layout(bases[i], max(0, n.h-2*n.py-n.top), true, true, true)
		}
	}
	if !n.row && !exactW {
		for i, c := range n.children {
			c.layout(max(0, n.w-2*n.px-n.pl), bases[i], true, true, false)
		}
	}
	position := 0.0
	for _, c := range n.children {
		c.x = n.px + n.pl
		c.y = n.py + n.top
		if n.row {
			c.x += position
			position += c.w + n.gap
		} else {
			position += c.margin
			c.y += position
			position += c.h + n.gap
		}
	}
}

// The frozen Yoga port rounds each relative origin and both absolute edges.
// Text origins floor; fractional Text dimensions ceil the far edge.
func (n *terminalHelpFlex) round(absX, absY float64) {
	x, y, w, h := n.x, n.y, n.w, n.h
	absoluteX, absoluteY := absX+x, absY+y
	text := n.text != ""
	n.x = terminalHelpRound(x, false, text)
	n.y = terminalHelpRound(y, false, text)
	wholeW := math.Abs(w-math.Round(w)) < .0001
	wholeH := math.Abs(h-math.Round(h)) < .0001
	n.w = terminalHelpRound(absoluteX+w, text && !wholeW, text && wholeW) - terminalHelpRound(absoluteX, false, text)
	n.h = terminalHelpRound(absoluteY+h, text && !wholeH, text && wholeH) - terminalHelpRound(absoluteY, false, text)
	for _, child := range n.children {
		child.round(absoluteX, absoluteY)
	}
}

func terminalHelpRound(value float64, ceil, floor bool) float64 {
	frac := value - math.Floor(value)
	if frac < .0001 {
		return math.Floor(value)
	}
	if frac > .9999 || ceil {
		return math.Ceil(value)
	}
	if floor {
		return math.Floor(value)
	}
	if frac >= .4999 {
		return math.Floor(value) + 1
	}
	return math.Floor(value)
}

func (n *terminalHelpFlex) sameContent(old *terminalHelpFlex) bool {
	if old == nil || n.key != old.key || n.text != old.text || n.row != old.row || len(n.children) != len(old.children) {
		return false
	}
	for i, child := range n.children {
		if !child.sameContent(old.children[i]) {
			return false
		}
	}
	return true
}

func (n *terminalHelpFlex) recordPositions(parentX, parentY int) {
	n.paintedX, n.paintedY = parentX+int(n.x), parentY+int(n.y)
	for _, child := range n.children {
		child.recordPositions(n.paintedX, n.paintedY)
	}
}

func (n *terminalHelpFlex) paint(grid [][]*terminalStatusLineCell, drawX, drawY int, old *terminalHelpFlex, previous [][]*terminalStatusLineCell) {
	text := n.text != ""
	x, y := drawX+int(n.x), drawY+int(n.y)
	n.paintedX, n.paintedY = x, y
	if old != nil && n.sameContent(old) && x == old.paintedX && y == old.paintedY && n.w == old.w && n.h == old.h && previous != nil {
		// Ink's clean-subtree blit copies only its Yoga rectangle, including
		// blank cells. Text overflowing that rectangle is not repainted.
		for row := max(0, y); row < min(len(grid), y+int(n.h)); row++ {
			for col := max(0, x); col < min(len(grid[row]), x+int(n.w)); col++ {
				grid[row][col] = previous[row][col]
			}
		}
		return
	}
	if text {
		width := min(int(n.w), len(grid[0])-x)
		for offset, line := range terminalMarkdownWrap(n.text, max(1, width)) {
			column := x
			for _, cell := range terminalStatusLineParse(line, false, "39") {
				cells := exportWidth(cell.text)
				if y+offset >= 0 && y+offset < len(grid) && column >= 0 && column+cells <= len(grid[0]) {
					copy := cell
					grid[y+offset][column] = &copy
					for j := 1; j < cells; j++ {
						grid[y+offset][column+j] = &terminalStatusLineCell{text: "", style: cell.style}
					}
				}
				column += cells
			}
		}
		return
	}
	if old != nil && n.key != old.key {
		old = nil
	}
	removed := old != nil && len(n.children) != len(old.children)
	if old != nil && !removed {
		for _, before := range old.children {
			if before.key == "" {
				continue
			}
			found := false
			for _, after := range n.children {
				if before.key == after.key {
					found = true
					break
				}
			}
			if !found {
				removed = true
				break
			}
		}
	}
	if removed {
		previous = nil
	}
	seenDirty := false
	for i, child := range n.children {
		var prior *terminalHelpFlex
		if old != nil && i < len(old.children) {
			prior = old.children[i]
		}
		dirty := !child.sameContent(prior)
		childPrevious := previous
		if seenDirty {
			childPrevious = nil
		}
		if child.h == 0 {
			sibling := i + 1
			if sibling == len(n.children) {
				sibling = i - 1
			}
			if sibling >= 0 && n.children[sibling].y == child.y {
				seenDirty = seenDirty || dirty
				continue
			}
		}
		child.paint(grid, x, y, prior, childPrevious)
		seenDirty = seenDirty || dirty
	}
}

func terminalHelpFlexRows(root *terminalHelpFlex, width, height int, cache **terminalHelpPaint) []string {
	root.layout(float64(width), float64(height), true, true, false)
	root.round(0, 0)
	root.recordPositions(0, 0)
	grid := make([][]*terminalStatusLineCell, height)
	for i := range grid {
		grid[i] = make([]*terminalStatusLineCell, width)
	}
	var previous [][]*terminalStatusLineCell
	var old *terminalHelpFlex
	if *cache != nil && len((*cache).cells) == height && len((*cache).cells[0]) == width {
		previous, old = (*cache).cells, (*cache).root
	}
	root.paint(grid, 0, 0, old, previous)
	*cache = &terminalHelpPaint{root: root, cells: grid}
	rows := make([]string, height)
	for y, line := range grid {
		cells := []terminalStatusLineCell{}
		for _, cell := range line {
			if cell == nil {
				cell = &terminalStatusLineCell{text: " ", style: terminalStatusLineStyle{fg: "39"}}
			}
			cells = append(cells, *cell)
		}
		rows[y] = terminalMarkdownPaintCells(cells, []string{strings.Repeat(" ", width)})[0]
	}
	return terminalMarkdownFinalRows(rows)
}
