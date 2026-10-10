package sessionhistory

import "bytes"

type indexedLine struct {
	start, end, parent int
	uuid               string
}
type lineRange struct{ start, end int }

func depthOne(data []byte, start int, candidates []int) int {
	depth, inString, escape, cursor := 0, false, false, 0
	for index := start; cursor < len(candidates); index++ {
		if index == candidates[cursor] {
			if depth == 1 && !inString {
				return candidates[cursor]
			}
			cursor++
		}
		value := data[index]
		if escape {
			escape = false
		} else if inString {
			if value == '\\' {
				escape = true
			} else if value == '"' {
				inString = false
			}
		} else if value == '"' {
			inString = true
		} else if value == '{' {
			depth++
		} else if value == '}' {
			depth--
		}
	}
	return candidates[len(candidates)-1]
}
func walkBeforeParse(data []byte) []byte {
	prefix, uuidKey, suffix := []byte(`{"parentUuid":`), []byte(`"uuid":"`), []byte(`","timestamp":"`)
	index, metadata, slots := []indexedLine{}, []lineRange{}, map[string]int{}
	for position := 0; position < len(data); {
		newline := bytes.IndexByte(data[position:], '\n')
		end := len(data)
		if newline >= 0 {
			end = position + newline + 1
		}
		if end-position > len(prefix) && bytes.HasPrefix(data[position:end], prefix) {
			parent := -1
			if data[position+len(prefix)] == '"' {
				parent = position + len(prefix) + 1
			}
			first, candidates := -1, []int{}
			for from := position; from < end; {
				next := bytes.Index(data[from:end], uuidKey)
				if next < 0 {
					break
				}
				next += from
				if first < 0 {
					first = next
				}
				after := next + len(uuidKey) + 36
				if after+len(suffix) <= end && bytes.HasPrefix(data[after:end], suffix) {
					candidates = append(candidates, next)
				}
				from = next + len(uuidKey)
			}
			selected := first
			if len(candidates) == 1 {
				selected = candidates[0]
			} else if len(candidates) > 1 {
				selected = depthOne(data, position, candidates)
			}
			if selected >= 0 {
				start := selected + len(uuidKey)
				stop := min(start+36, len(data))
				uuid := string(data[start:stop])
				slots[uuid] = len(index)
				index = append(index, indexedLine{position, end, parent, uuid})
			} else {
				metadata = append(metadata, lineRange{position, end})
			}
		} else {
			metadata = append(metadata, lineRange{position, end})
		}
		position = end
	}
	leaf := -1
	for cursor := len(index) - 1; cursor >= 0; cursor-- {
		line := index[cursor]
		if !bytes.Contains(data[line.start:line.end], []byte(`"isSidechain":true`)) {
			leaf = cursor
			break
		}
	}
	if leaf < 0 {
		return data
	}
	seen, keep := map[int]bool{}, map[int]bool{}
	chainBytes := 0
	for slot := leaf; ; {
		if seen[slot] {
			break
		}
		seen[slot], keep[index[slot].start] = true, true
		line := index[slot]
		chainBytes += line.end - line.start
		if line.parent < 0 {
			break
		}
		parent := string(data[line.parent:min(line.parent+36, len(data))])
		next, exists := slots[parent]
		if !exists {
			break
		}
		slot = next
	}
	if len(data)-chainBytes < len(data)>>1 {
		return data
	}
	result, meta := []byte{}, 0
	for _, line := range index {
		for meta < len(metadata) && metadata[meta].start < line.start {
			result = append(result, data[metadata[meta].start:metadata[meta].end]...)
			meta++
		}
		if keep[line.start] {
			result = append(result, data[line.start:line.end]...)
		}
	}
	for ; meta < len(metadata); meta++ {
		result = append(result, data[metadata[meta].start:metadata[meta].end]...)
	}
	return result
}
