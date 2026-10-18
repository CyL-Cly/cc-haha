package tui

type vimRange struct{ Start, End int }

func vimTextObject(text []uint16, offset int, kind string, inner bool) *vimRange {
	if kind == "w" || kind == "W" {
		graphemes := vimGraphemes(text)
		if len(graphemes) == 0 {
			return nil
		}
		index := len(graphemes) - 1
		for i, g := range graphemes {
			if offset >= g.Start && offset < g.End {
				index = i
				break
			}
		}
		at := func(i int) []uint16 {
			if i < 0 || i >= len(graphemes) {
				return nil
			}
			return graphemes[i].Units
		}
		position := func(i int) int {
			if i >= len(graphemes) {
				return len(text)
			}
			return graphemes[max(0, i)].Start
		}
		isWord := func(i int) bool {
			if kind == "W" {
				return !vimWhitespace(at(i))
			}
			return vimWord(at(i))
		}
		isSpace := func(i int) bool { return vimWhitespace(at(i)) }
		start, end := index, index
		if isWord(index) {
			for start > 0 && isWord(start-1) {
				start--
			}
			for end < len(graphemes) && isWord(end) {
				end++
			}
		} else if isSpace(index) {
			for start > 0 && isSpace(start-1) {
				start--
			}
			for end < len(graphemes) && isSpace(end) {
				end++
			}
			return &vimRange{position(start), position(end)}
		} else if vimPunctuation(at(index)) {
			for start > 0 && vimPunctuation(at(start-1)) {
				start--
			}
			for end < len(graphemes) && vimPunctuation(at(end)) {
				end++
			}
		}
		if !inner {
			if end < len(graphemes) && isSpace(end) {
				for end < len(graphemes) && isSpace(end) {
					end++
				}
			} else if start > 0 && isSpace(start-1) {
				for start > 0 && isSpace(start-1) {
					start--
				}
			}
		}
		return &vimRange{position(start), position(end)}
	}
	pairs := map[string][2]uint16{"(": {'(', ')'}, ")": {'(', ')'}, "b": {'(', ')'}, "[": {'[', ']'}, "]": {'[', ']'}, "{": {'{', '}'}, "}": {'{', '}'}, "B": {'{', '}'}, "<": {'<', '>'}, ">": {'<', '>'}, "\"": {'"', '"'}, "'": {'\'', '\''}, "`": {'`', '`'}}
	pair, ok := pairs[kind]
	if !ok {
		return nil
	}
	if pair[0] == pair[1] {
		cursor := vimCursor{text: text, offset: offset}
		start, end := cursor.lineStart(offset), cursor.lineEnd(offset)
		positions := []int{}
		for i := start; i < end; i++ {
			if text[i] == pair[0] {
				positions = append(positions, i)
			}
		}
		for i := 0; i+1 < len(positions); i += 2 {
			if positions[i] <= offset && offset <= positions[i+1] {
				if inner {
					return &vimRange{positions[i] + 1, positions[i+1]}
				}
				return &vimRange{positions[i], positions[i+1] + 1}
			}
		}
		return nil
	}
	depth, start := 0, -1
	for i := min(offset, len(text)-1); i >= 0; i-- {
		if text[i] == pair[1] && i != offset {
			depth++
		} else if text[i] == pair[0] {
			if depth == 0 {
				start = i
				break
			}
			depth--
		}
	}
	if start < 0 {
		return nil
	}
	depth = 0
	for i := start + 1; i < len(text); i++ {
		if text[i] == pair[0] {
			depth++
		} else if text[i] == pair[1] {
			if depth == 0 {
				if inner {
					return &vimRange{start + 1, i}
				}
				return &vimRange{start, i + 1}
			}
			depth--
		}
	}
	return nil
}
