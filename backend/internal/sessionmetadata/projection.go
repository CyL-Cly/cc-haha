package sessionmetadata

import (
	"bytes"
	"encoding/json"
	"strconv"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

const metadataBytes = 128 * 1024
const previewChars = 4096
const maxDepth = 128
const maxKeyChars = 256
const unselected = "\x00unselected"

type tokenKind uint8

const (
	startObject tokenKind = iota
	endObject
	startArray
	endArray
	startKey
	endKey
	startString
	endString
	startNumber
	endNumber
	stringChunk
	numberChunk
	trueValue
	falseValue
	nullValue
)

// Chunks refer to the current input buffer. Escapes and cross-chunk UTF-8 use
// the inline code-unit pair, so discarded strings never allocate their body.
type token struct {
	kind  tokenKind
	raw   []byte
	units [2]uint16
	count int
}
type value struct {
	kind      byte
	scalar    []uint16
	keys      []string
	fields    map[string]*value
	items     []*value
	truncated map[string]bool
}

func (node *value) write(output *bytes.Buffer) {
	switch node.kind {
	case '{':
		output.WriteByte('{')
		for index, key := range node.keys {
			if index != 0 {
				output.WriteByte(',')
			}
			output.WriteString(key)
			output.WriteByte(':')
			node.fields[key].write(output)
		}
		output.WriteByte('}')
	case '[':
		output.WriteByte('[')
		for index, item := range node.items {
			if index != 0 {
				output.WriteByte(',')
			}
			item.write(output)
		}
		output.WriteByte(']')
	case '"':
		output.Write(protocol.QuoteJavaScriptUTF16(node.scalar))
	case 'n':
		for _, unit := range node.scalar {
			output.WriteByte(byte(unit))
		}
	case 't':
		output.WriteString("true")
	case 'f':
		output.WriteString("false")
	default:
		output.WriteString("null")
	}
}
func (node *value) field(name string) *value {
	if node == nil || node.kind != '{' {
		return nil
	}
	return node.fields[strconv.Quote(name)]
}
func (node *value) cut(name string) bool {
	return node != nil && node.truncated[strconv.Quote(name)]
}

type frame struct {
	path  []string
	value *value
	key   []uint16
	index int
}
type projection struct {
	profile  *BoundedProfile
	frames   []frame
	root     *value
	bytes    int
	scalar   []uint16
	path     []string
	mode     tokenKind
	overflow bool
}

func metadataLimit() error {
	return &sessionstore.APIError{StatusCode: 413, Code: "SESSION_METADATA_TOO_LARGE", Message: "Session metadata exceeds its resource budget"}
}
func selected(path []string) bool {
	for _, part := range path {
		if part == unselected {
			return false
		}
	}
	if len(path) == 0 {
		return true
	}
	switch path[0] {
	case "type", "subtype", "uuid", "parentUuid", "parent_tool_use_id", "isSidechain", "isMeta",
		"cwd", "timestamp", "workDir", "repository", "worktreeSession", "permissionMode",
		"runtimeProviderId", "runtimeModelId", "effortLevel", "openAIFastMode", "customTitle", "aiTitle", "entrypoint", "content":
		return true
	case "message":
		if len(path) == 1 {
			return true
		}
		if path[1] == "role" {
			return len(path) == 2
		}
		if path[1] != "content" {
			return false
		}
		if len(path) <= 3 {
			return true
		}
		if len(path) == 4 {
			switch path[3] {
			case "type", "name", "id", "tool_use_id", "text":
				return true
			}
		}
	}
	return false
}
func preview(path []string) bool {
	return len(path) == 1 && path[0] == "content" ||
		len(path) >= 2 && path[0] == "message" && path[1] == "content" &&
			(len(path) == 2 || len(path) == 4 && path[3] == "text")
}
func (p *projection) nextPath() []string {
	if len(p.frames) == 0 {
		return nil
	}
	parent := &p.frames[len(p.frames)-1]
	path := append([]string(nil), parent.path...)
	key := string(utf16.Decode(parent.key))
	if parent.value != nil && parent.value.kind == '[' {
		key = strconv.Itoa(parent.index)
	}
	return append(path, key)
}
func utf8Bytes(units []uint16) int {
	bytes := 0
	for index := 0; index < len(units); index++ {
		unit := units[index]
		switch {
		case unit < 0x80:
			bytes++
		case unit < 0x800:
			bytes += 2
		case unit >= 0xd800 && unit <= 0xdbff && index+1 < len(units) && units[index+1] >= 0xdc00 && units[index+1] <= 0xdfff:
			bytes += 4
			index++
		default:
			bytes += 3
		}
	}
	return bytes
}
func (p *projection) charge(size int) error {
	p.bytes += size
	if p.bytes > p.budget() {
		return p.limit()
	}
	return nil
}
func (p *projection) attach(node *value, path []string, cut bool) error {
	var parent *frame
	if len(p.frames) != 0 {
		parent = &p.frames[len(p.frames)-1]
	}
	if p.selected(path) {
		if parent == nil {
			p.root = node
		} else if parent.value != nil {
			cost := 16
			if parent.value.kind != '[' {
				cost += utf8Bytes(parent.key)
			}
			if err := p.charge(cost); err != nil {
				return err
			}
			slot := string(protocol.QuoteJavaScriptUTF16(parent.key))
			if parent.value.kind == '[' {
				slot = strconv.Quote(strconv.Itoa(len(parent.value.items)))
				parent.value.items = append(parent.value.items, node)
			} else {
				if parent.value.fields == nil {
					parent.value.fields = map[string]*value{}
				}
				if _, exists := parent.value.fields[slot]; !exists {
					parent.value.keys = append(parent.value.keys, slot)
				}
				parent.value.fields[slot] = node
			}
			if cut {
				if parent.value.truncated == nil {
					parent.value.truncated = map[string]bool{}
				}
				parent.value.truncated[slot] = true
			} else {
				delete(parent.value.truncated, slot)
			}
		}
	}
	if parent != nil {
		parent.index++
	}
	return nil
}
func (p *projection) chunk(item token) error {
	if p.mode != startKey && !p.selected(p.path) {
		return nil
	}
	bound := p.budget()
	isPreview := p.mode != startKey && p.preview(p.path)
	if p.mode == startKey {
		bound = maxKeyChars
	} else if isPreview {
		bound = p.previewBudget()
	}
	put := func(unit uint16) error {
		if len(p.scalar) >= bound {
			p.overflow = true
			if p.mode != startKey && !isPreview {
				return p.limit()
			}
			return nil
		}
		p.scalar = append(p.scalar, unit)
		return nil
	}
	// Fast discarded suffix: no decoding or allocation once a preview/key fills.
	if len(p.scalar) == bound && (len(item.raw) != 0 || item.count != 0) {
		p.overflow = true
		if p.mode != startKey && !isPreview {
			return p.limit()
		}
		return nil
	}
	for _, char := range item.raw {
		if err := put(uint16(char)); err != nil {
			return err
		}
	}
	for _, unit := range item.units[:item.count] {
		if err := put(unit); err != nil {
			return err
		}
	}
	return nil
}
func (p *projection) token(item token) error {
	switch item.kind {
	case startObject, startArray:
		if len(p.frames) >= maxDepth {
			return p.limit()
		}
		path := p.nextPath()
		var node *value
		if p.selected(path) {
			kind := byte('{')
			if item.kind == startArray {
				kind = '['
			}
			node = &value{kind: kind}
		}
		if err := p.attach(node, path, false); err != nil {
			return err
		}
		p.frames = append(p.frames, frame{path: path, value: node})
	case endObject, endArray:
		p.frames = p.frames[:len(p.frames)-1]
	case startKey, startString, startNumber:
		p.mode = item.kind
		p.scalar = p.scalar[:0]
		p.overflow = false
		if item.kind != startKey {
			p.path = p.nextPath()
		}
	case stringChunk, numberChunk:
		return p.chunk(item)
	case endKey:
		parent := &p.frames[len(p.frames)-1]
		if p.overflow && parent.value != nil && len(parent.path) != 0 && parent.path[0] != "message" {
			return p.limit()
		}
		if p.overflow {
			parent.key = utf16.Encode([]rune(unselected))
		} else {
			parent.key = append(parent.key[:0], p.scalar...)
		}
	case endString, endNumber:
		if p.selected(p.path) {
			if err := p.charge(utf8Bytes(p.scalar)); err != nil {
				return err
			}
		}
		var node *value
		if p.selected(p.path) {
			kind := byte('"')
			if item.kind == endNumber {
				kind = 'n'
			}
			node = &value{kind: kind, scalar: append([]uint16(nil), p.scalar...)}
		}
		return p.attach(node, p.path, p.overflow && len(p.path) != 0 && p.path[0] == "message")
	case trueValue, falseValue, nullValue:
		kind := byte('0')
		if item.kind == trueValue {
			kind = 't'
		} else if item.kind == falseValue {
			kind = 'f'
		}
		return p.attach(&value{kind: kind}, p.nextPath(), false)
	}
	return nil
}
func (p *projection) entry() Entry {
	var raw bytes.Buffer
	p.root.write(&raw)
	message := p.root.field("message")
	truncated := message.cut("content")
	if content := message.field("content"); content != nil && content.kind == '[' {
		for _, block := range content.items {
			if block.cut("text") {
				truncated = true
			}
		}
	}
	return Entry{Value: json.RawMessage(raw.Bytes()), TextTruncated: truncated}
}
