package usercontext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"go.yaml.in/yaml/v3"
)

type MemoryFile struct {
	Path                   string   `json:"path"`
	Type                   string   `json:"type"`
	Content                string   `json:"content"`
	Parent                 string   `json:"parent,omitempty"`
	Globs                  []string `json:"globs,omitempty"`
	ContentDiffersFromDisk bool     `json:"contentDiffersFromDisk"`
	RawContent             string   `json:"rawContent,omitempty"`
	contentCodeUnits       []uint16
}

func (file MemoryFile) PromptContent() json.RawMessage {
	if file.contentCodeUnits != nil {
		return protocol.QuoteJavaScriptUTF16(file.contentCodeUnits)
	}
	return quote(file.Content)
}

func (file MemoryFile) MarshalJSON() ([]byte, error) {
	type ordinary MemoryFile
	return json.Marshal(struct {
		ordinary
		Content json.RawMessage `json:"content"`
	}{ordinary(file), file.PromptContent()})
}

var frontmatterPattern = regexp.MustCompile(`(?m)^---[\t ]*\r?\n([\s\S]*?)^---[\t ]*(?:\r?\n[\t \r\n]*|$)`)
var yamlKeyPattern = regexp.MustCompile(`^([a-zA-Z_-]+):\s+(.+)$`)
var commentPattern = regexp.MustCompile(`<!--[\s\S]*?-->`)
var includePattern = regexp.MustCompile(`(?:^|\s)@((?:[^\s\\]|\\ )+)`)
var validRelativeInclude = regexp.MustCompile(`^[a-zA-Z0-9._-]`)

func trimMemory(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r >= 9 && r <= 13 || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
}
func stringFromUnits(units []uint16) string { return string(utf16.Decode(units)) }

// ParseMarkdownFrontmatter is the original file-level markdown configuration
// parser, shared with output styles; it receives raw native file bytes.
func ParseMarkdownFrontmatter(raw string) (map[string]any, string) {
	matches := frontmatterPattern.FindStringSubmatchIndex(raw)
	if matches == nil {
		return nil, raw
	}
	yamlText := raw[matches[2]:matches[3]]
	var value map[string]any
	if err := yaml.Unmarshal([]byte(yamlText), &value); err != nil {
		lines := strings.Split(yamlText, "\n")
		for i, line := range lines {
			m := yamlKeyPattern.FindStringSubmatch(line)
			if len(m) != 3 {
				continue
			}
			v := m[2]
			quoted := len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'')
			if !quoted && (strings.ContainsAny(v, "{}[]*&#!|>%@`") || strings.Contains(v, ": ")) {
				lines[i] = m[1] + ": " + strconv.Quote(v)
			}
		}
		value = nil
		_ = yaml.Unmarshal([]byte(strings.Join(lines, "\n")), &value)
	}
	// The original multiline regex slices by match length, even when the
	// opening delimiter occurs after preceding text. Retain that behavior.
	content := raw[matches[1]-matches[0]:]
	return value, content
}

func frontmatter(raw string) (string, []string) {
	value, content := ParseMarkdownFrontmatter(raw)
	var rawPatterns []string
	switch paths := value["paths"].(type) {
	case string:
		rawPatterns = []string{paths}
	case []any:
		for _, p := range paths {
			if v, ok := p.(string); ok {
				rawPatterns = append(rawPatterns, v)
			}
		}
	}
	patterns := expandMemoryPatterns(rawPatterns)
	out := []string{}
	all := true
	for _, p := range patterns {
		p = strings.TrimSuffix(p, "/**")
		if p == "" {
			continue
		}
		out = append(out, p)
		all = all && p == "**"
	}
	if len(out) == 0 || all {
		return content, nil
	}
	return content, out
}
func expandMemoryPatterns(inputs []string) []string {
	var patterns []string
	for _, input := range inputs {
		depth, start := 0, 0
		for i, c := range input {
			if c == '{' {
				depth++
			} else if c == '}' {
				depth--
			} else if c == ',' && depth == 0 {
				if p := trimMemory(input[start:i]); p != "" {
					patterns = append(patterns, p)
				}
				start = i + 1
			}
		}
		if p := trimMemory(input[start:]); p != "" {
			patterns = append(patterns, p)
		}
	}
	output := []string{}
	spent := 0
	exhausted := false
	brace := regexp.MustCompile(`^([^{]*)\{([^}]+)\}(.*)$`)
	for _, pattern := range patterns {
		if exhausted {
			output = append(output, pattern)
			continue
		}
		pending := []string{pattern}
		expanded := []string{}
		for len(pending) > 0 {
			current := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			spent += len(utf16.Encode([]rune(current)))
			m := brace.FindStringSubmatch(current)
			if m == nil {
				expanded = append(expanded, current)
				continue
			}
			parts := strings.Split(m[2], ",")
			projected := len(output) + len(expanded) + len(pending) + len(parts)
			if spent > 4*1024*1024 || projected > 1000 || projected*len(utf16.Encode([]rune(pattern))) > 4*1024*1024-spent {
				exhausted = true
				expanded = []string{pattern}
				break
			}
			for i := len(parts) - 1; i >= 0; i-- {
				pending = append(pending, m[1]+trimMemory(parts[i])+m[3])
			}
		}
		output = append(output, expanded...)
	}
	return output
}

var textExtensions = map[string]bool{}

func init() {
	for _, e := range strings.Fields(".md .txt .text .json .yaml .yml .toml .xml .csv .html .htm .css .scss .sass .less .js .ts .tsx .jsx .mjs .cjs .mts .cts .py .pyi .pyw .rb .erb .rake .go .rs .java .kt .kts .scala .c .cpp .cc .cxx .h .hpp .hxx .cs .swift .sh .bash .zsh .fish .ps1 .bat .cmd .env .ini .cfg .conf .config .properties .sql .graphql .gql .proto .vue .svelte .astro .ejs .hbs .pug .jade .php .pl .pm .lua .r .dart .ex .exs .erl .hrl .clj .cljs .cljc .edn .hs .lhs .elm .ml .mli .f .f90 .f95 .for .cmake .make .makefile .gradle .sbt .rst .adoc .asciidoc .org .tex .latex .lock .log .diff .patch") {
		textExtensions[e] = true
	}
}

type memorySpan struct {
	start, end  int
	replacement []byte
}

func parseMemory(raw, path, kind, includeBase, home string) (*MemoryFile, []string) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != "" && !textExtensions[ext] {
		return nil, nil
	}
	without, globs := frontmatter(raw)
	hasComment := strings.Contains(without, "<!--")
	source := []byte(without)
	if hasComment {
		source = bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n"))
		source = bytes.ReplaceAll(source, []byte("\r"), []byte("\n"))
	}
	content := source
	includes := []string{}
	seen := map[string]bool{}
	spans := []memorySpan{}
	extract := func(value []byte) {
		if includeBase == "" {
			return
		}
		for _, m := range includePattern.FindAllStringSubmatch(string(value), -1) {
			p := strings.SplitN(m[1], "#", 2)[0]
			p = strings.ReplaceAll(p, `\ `, " ")
			if p == "" || !(strings.HasPrefix(p, "./") || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "/") && p != "/" || !strings.HasPrefix(p, "@") && validRelativeInclude.MatchString(p)) {
				continue
			}
			if strings.HasPrefix(p, "~/") {
				p = filepath.Join(home, p[2:])
			} else {
				p = resolveRelative(filepath.Dir(includeBase), p)
			}
			if !seen[p] {
				seen[p] = true
				includes = append(includes, p)
			}
		}
	}
	if hasComment || includeBase != "" {
		document := goldmark.New().Parser().Parse(text.NewReader(source))
		_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			switch n := node.(type) {
			case *ast.CodeBlock, *ast.FencedCodeBlock, *ast.CodeSpan:
				return ast.WalkSkipChildren, nil
			case *ast.HTMLBlock:
				rawBlock := n.Text(source)
				if strings.HasPrefix(strings.TrimLeft(string(rawBlock), " \t\r\n"), "<!--") && bytes.Contains(rawBlock, []byte("-->")) {
					residue := commentPattern.ReplaceAll(rawBlock, nil)
					extract(residue)
					if hasComment && n.Lines().Len() > 0 {
						start := n.Lines().At(0).Start
						end := n.Lines().At(n.Lines().Len() - 1).Stop
						if n.HasClosure() {
							end = n.ClosureLine.Stop
						}
						// marked's HTML token includes its following blank lines.
						initialEnd := end
						for end < len(source) {
							next := bytes.IndexByte(source[end:], '\n')
							if next < 0 {
								break
							}
							lineEnd := end + next + 1
							if trimMemory(string(source[end:lineEnd])) != "" {
								break
							}
							end = lineEnd
						}
						if trimMemory(string(residue)) == "" {
							residue = nil
						} else {
							residue = append(residue, source[initialEnd:end]...)
						}
						spans = append(spans, memorySpan{start, end, residue})
					}
				}
				return ast.WalkSkipChildren, nil
			case *ast.RawHTML:
				rawInline := n.Text(source)
				if strings.HasPrefix(strings.TrimLeft(string(rawInline), " \t\r\n"), "<!--") && bytes.Contains(rawInline, []byte("-->")) {
					extract(commentPattern.ReplaceAll(rawInline, nil))
				}
				return ast.WalkSkipChildren, nil
			case *ast.Text:
				if _, adjacent := n.PreviousSibling().(*ast.Text); adjacent {
					break
				}
				end := n.Segment.Stop
				for next := n.NextSibling(); next != nil; next = next.NextSibling() {
					textNode, adjacent := next.(*ast.Text)
					if !adjacent {
						break
					}
					end = textNode.Segment.Stop
				}
				extract(source[n.Segment.Start:end])
			}
			return ast.WalkContinue, nil
		})
		if len(spans) > 0 {
			var out bytes.Buffer
			at := 0
			for _, span := range spans {
				if span.start < at || span.end > len(source) {
					continue
				}
				out.Write(source[at:span.start])
				out.Write(span.replacement)
				at = span.end
			}
			out.Write(source[at:])
			content = out.Bytes()
		}
	}
	final := string(content)
	var exact []uint16
	if kind == "AutoMem" || kind == "TeamMem" {
		units := truncateMemoryUnits(final)
		final = stringFromUnits(units)
		encoded := utf16.Encode([]rune(final))
		if len(encoded) != len(units) {
			exact = units
		} else {
			for i := range units {
				if encoded[i] != units[i] {
					exact = units
					break
				}
			}
		}
	}
	file := &MemoryFile{Path: path, Type: kind, Content: final, Globs: globs, ContentDiffersFromDisk: final != raw, contentCodeUnits: exact}
	if file.ContentDiffersFromDisk {
		file.RawContent = raw
	}
	return file, includes
}
func truncateMemory(raw string) string {
	return stringFromUnits(truncateMemoryUnits(raw))
}
func truncateMemoryUnits(raw string) []uint16 {
	trimmed := trimMemory(raw)
	lines := strings.Split(trimmed, "\n")
	units := utf16.Encode([]rune(trimmed))
	lineTruncated, byteTruncated := len(lines) > 200, len(units) > 25000
	if !lineTruncated && !byteTruncated {
		return units
	}
	result := trimmed
	if lineTruncated {
		result = strings.Join(lines[:200], "\n")
	}
	u := utf16.Encode([]rune(result))
	if len(u) > 25000 {
		cut := 25000
		for i := 25000; i > 0; i-- {
			if u[i] == '\n' {
				cut = i
				break
			}
		}
		u = u[:cut]
	}
	reason := fmt.Sprintf("%d lines and %s", len(lines), memorySize(len(units)))
	if byteTruncated && !lineTruncated {
		reason = memorySize(len(units)) + " (limit: " + memorySize(25000) + ") — index entries are too long"
	} else if lineTruncated && !byteTruncated {
		reason = fmt.Sprintf("%d lines (limit: 200)", len(lines))
	}
	return append(u, utf16.Encode([]rune("\n\n> WARNING: MEMORY.md is "+reason+". Only part of it was loaded. Keep index entries to one line under ~200 chars; move detail into topic files."))...)
}
func memorySize(size int) string {
	if size < 1024 {
		return fmt.Sprintf("%d bytes", size)
	}
	amount := float64(size) / 1024
	unit := "KB"
	if amount >= 1024 {
		amount /= 1024
		unit = "MB"
	}
	if amount >= 1024 {
		amount /= 1024
		unit = "GB"
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", amount), ".0") + unit
}
