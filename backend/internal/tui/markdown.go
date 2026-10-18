package tui

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var terminalMarkdownParser = goldmark.New(goldmark.WithExtensions(extension.GFM))
var terminalMarkdownSyntax = regexp.MustCompile("[#*`|[>\\-_~]|\\n\\n|^\\d+\\. |\\n\\d+\\. ")

func terminalMarkdownSource(value string) string {
	// These four prompt XML blocks are the original Markdown presentation
	// exclusions. Retained records and model input never pass through this.
	for _, tag := range []string{"commit_analysis", "context", "function_analysis", "pr_analysis"} {
		value = regexp.MustCompile(`(?s)<`+tag+`>.*?</`+tag+`>\n?`).ReplaceAllString(value, "")
	}
	return protocol.TrimJavaScriptString(displayText(value))
}

func terminalMarkdownHasSyntax(value string) bool {
	if strings.Contains(value, "://") {
		return true
	}
	units := utf16.Encode([]rune(value))
	if len(units) > 500 {
		value = string(utf16.Decode(units[:500]))
	}
	return terminalMarkdownSyntax.MatchString(value)
}

type terminalMarkdownRenderer struct {
	source []byte
	theme  string
}

func terminalMarkdownStyle(value, start, end string) string {
	if value == "" {
		return ""
	}
	return start + value + end
}

func (r terminalMarkdownRenderer) children(node ast.Node, depth int) string {
	var result strings.Builder
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		result.WriteString(r.node(child, depth))
	}
	return result.String()
}

func (r terminalMarkdownRenderer) node(node ast.Node, depth int) string {
	switch n := node.(type) {
	case *ast.Text:
		value := string(util.UnescapePunctuations(n.Segment.Value(r.source)))
		if n.HardLineBreak() {
			value = strings.TrimRight(value, " \t")
		}
		if n.SoftLineBreak() || n.HardLineBreak() {
			value += "\n"
		}
		return value
	case *ast.String:
		return string(n.Value)
	case *ast.CodeSpan:
		value := strings.ReplaceAll(r.children(node, depth), "\n", " ")
		return terminalMarkdownStyle(value, TerminalThemeRoleEscape(r.theme, "permission", false), "\x1b[39m")
	case *ast.Emphasis:
		start, end := "\x1b[3m", "\x1b[23m"
		if n.Level == 2 {
			start, end = "\x1b[1m", "\x1b[22m"
		}
		return terminalMarkdownStyle(r.children(node, depth), start, end)
	case *ast.Heading:
		start, end := "\x1b[1m", "\x1b[22m"
		if n.Level == 1 {
			start, end = "\x1b[1m\x1b[3m\x1b[4m", "\x1b[24m\x1b[23m\x1b[22m"
		}
		return terminalMarkdownStyle(r.children(node, depth), start, end) + "\n\n"
	case *ast.Paragraph, *ast.TextBlock:
		return r.children(node, depth) + "\n"
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		var out strings.Builder
		for i := 0; i < node.Lines().Len(); i++ {
			segment := node.Lines().At(i)
			out.Write(segment.Value(r.source))
		}
		return strings.TrimSuffix(out.String(), "\n") + "\n"
	case *ast.Blockquote:
		inner := r.blocks(node, 0)
		lines := strings.Split(inner, "\n")
		for i, line := range lines {
			if protocol.TrimJavaScriptString(ansi.Strip(line)) != "" {
				lines[i] = "\x1b[2m▎\x1b[22m " + terminalMarkdownStyle(line, "\x1b[3m", "\x1b[23m")
			}
		}
		return strings.Join(lines, "\n")
	case *ast.ThematicBreak:
		return "---"
	case *ast.Link:
		return strings.TrimPrefix(string(n.Destination), "mailto:")
	case *ast.AutoLink:
		return string(n.URL(r.source))
	case *ast.Image:
		return string(n.Destination)
	case *extast.Strikethrough:
		return "~~" + r.children(node, depth) + "~~"
	case *ast.RawHTML, *ast.HTMLBlock:
		return ""
	case *ast.List:
		out := ""
		index := n.Start
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			prefix := "-"
			if n.IsOrdered() {
				prefix = terminalMarkdownListNumber(depth+1, index) + "."
				index++
			}
			for block := child.FirstChild(); block != nil; block = block.NextSibling() {
				indent := strings.Repeat("  ", depth)
				if _, nested := block.(*ast.List); nested {
					out += r.node(block, depth+1)
				} else {
					body := r.node(block, depth+1)
					if body != "" {
						out += indent + prefix + " " + body
					}
				}
			}
		}
		return out
	case *extast.TaskCheckBox:
		return "" // The original token formatter does not display a task marker.
	default:
		return r.children(node, depth)
	}
}

func (r terminalMarkdownRenderer) blocks(parent ast.Node, depth int) string {
	var out strings.Builder
	for node := parent.FirstChild(); node != nil; node = node.NextSibling() {
		_, previousHeading := node.PreviousSibling().(*ast.Heading)
		if node.PreviousSibling() != nil && node.HasBlankPreviousLines() && !previousHeading {
			out.WriteByte('\n')
		}
		out.WriteString(r.node(node, depth))
	}
	return out.String()
}

func terminalMarkdownListNumber(depth, value int) string {
	if depth == 2 && value > 0 {
		out := ""
		for value > 0 {
			value--
			out = string(rune('a'+value%26)) + out
			value /= 26
		}
		return out
	}
	if depth == 3 && value > 0 {
		out := ""
		for _, pair := range []struct {
			n    int
			text string
		}{{1000, "m"}, {900, "cm"}, {500, "d"}, {400, "cd"}, {100, "c"}, {90, "xc"}, {50, "l"}, {40, "xl"}, {10, "x"}, {9, "ix"}, {5, "v"}, {4, "iv"}, {1, "i"}} {
			for value >= pair.n {
				out += pair.text
				value -= pair.n
			}
		}
		return out
	}
	return fmt.Sprint(value)
}

// TerminalMarkdownRows renders structured Markdown tokens; only formatting
// generated by this renderer reaches the terminal. It never trusts input ANSI.
func TerminalMarkdownRows(value string, width, terminalWidth int, theme string) []string {
	return terminalMarkdownFinalRows(terminalMarkdownRows(value, width, terminalWidth, theme))
}

func terminalMarkdownRows(value string, width, terminalWidth int, theme string) []string {
	value = terminalMarkdownSource(value)
	if !terminalMarkdownHasSyntax(value) {
		return exportWrapWords(value, max(1, width))
	}
	source := []byte(value)
	document := terminalMarkdownParser.Parser().Parse(text.NewReader(source))
	r := terminalMarkdownRenderer{source, theme}
	rows := []string{}
	var buffer strings.Builder
	flush := func() {
		if body := protocol.TrimJavaScriptString(buffer.String()); body != "" {
			if len(rows) > 0 {
				rows = append(rows, "")
			}
			rows = append(rows, terminalMarkdownWrap(body, width)...)
		}
		buffer.Reset()
	}
	for node := document.FirstChild(); node != nil; node = node.NextSibling() {
		if table, ok := node.(*extast.Table); ok {
			flush()
			if len(rows) > 0 {
				rows = append(rows, "")
			}
			rows = append(rows, r.table(table, width, terminalWidth)...)
			continue
		}
		_, previousHeading := node.PreviousSibling().(*ast.Heading)
		if node.PreviousSibling() != nil && node.HasBlankPreviousLines() && buffer.Len() > 0 && !previousHeading {
			buffer.WriteByte('\n')
		}
		buffer.WriteString(r.node(node, 0))
	}
	flush()
	return rows
}

func terminalMarkdownWrap(value string, width int) []string {
	// The Source Ansi component measures/wraps plain cells, then carries its
	// style spans through those measured offsets.
	rows := []string{}
	cells := terminalStatusLineParse(value, false, "39")
	line := []terminalStatusLineCell{}
	flush := func() {
		plain := strings.Builder{}
		for _, cell := range line {
			plain.WriteString(cell.text)
		}
		rows = append(rows, terminalMarkdownPaintCells(line, exportWrapWords(plain.String(), max(1, width)))...)
		line = nil
	}
	for _, cell := range cells {
		if cell.text == "\n" {
			flush()
		} else {
			line = append(line, cell)
		}
	}
	flush()
	return rows
}

func terminalMarkdownApplyStyles(value string, rows []string) []string {
	cells := terminalStatusLineParse(value, false, "39")
	return terminalMarkdownPaintCells(cells, rows)
}

func terminalMarkdownPaintCells(cells []terminalStatusLineCell, rows []string) []string {
	position := 0
	for i, row := range rows {
		width := exportWidth(row)
		painted, column := strings.Builder{}, 0
		for position < len(cells) && column < width {
			cell := cells[position]
			position++
			column += exportWidth(cell.text)
			if cell.style.sgr() == "39" {
				painted.WriteString(cell.text)
			} else {
				painted.WriteString("\x1b[0;" + cell.style.sgr() + "m" + cell.text + "\x1b[0m")
			}
		}
		rows[i] = painted.String()
	}
	return rows
}

func terminalMarkdownFinalRows(rows []string) []string {
	for i, row := range rows {
		cells := terminalStatusLineParse(row, false, "39")
		for len(cells) > 0 && cells[len(cells)-1].text == " " && cells[len(cells)-1].style.sgr() == "39" {
			cells = cells[:len(cells)-1]
		}
		width := 0
		for _, cell := range cells {
			width += exportWidth(cell.text)
		}
		rows[i] = terminalMarkdownPaintCells(cells, []string{strings.Repeat(" ", width)})[0]
	}
	return rows
}
