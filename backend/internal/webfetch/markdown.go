package webfetch

// The rule and whitespace algorithm follows Turndown's MIT-licensed defaults.
// HTML parsing uses the HTML5 Go parser; no JavaScript business is executed.
import (
	"fmt"
	"golang.org/x/net/html"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

var blocks = wordset("address article aside audio blockquote body canvas center dd dir div dl dt fieldset figcaption figure footer form frameset h1 h2 h3 h4 h5 h6 header hgroup hr html isindex li main menu nav noframes noscript ol output p pre section table tbody td tfoot th thead tr ul")
var voids = wordset("area base br col command embed hr img input keygen link meta param source track wbr")
var meaningful = wordset("a table thead tbody tfoot th td iframe script audio video")

func wordset(words string) map[string]bool {
	out := map[string]bool{}
	for _, word := range strings.Fields(words) {
		out[word] = true
	}
	return out
}
func textContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		b.WriteString(textContent(child))
	}
	return b.String()
}
func descendant(n *html.Node, set map[string]bool) bool {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && set[child.Data] || descendant(child, set) {
			return true
		}
	}
	return false
}
func nodeBlock(n *html.Node) bool { return n != nil && n.Type == html.ElementNode && blocks[n.Data] }

var collapseSpaces = regexp.MustCompile(`[ \r\n\t]+`)

func collapseHTML(root *html.Node) {
	var prevText, prev *html.Node
	keepLeading := false
	next := func(previous, current *html.Node) *html.Node {
		if previous != nil && previous.Parent == current || current.Data == "pre" {
			if current.NextSibling != nil {
				return current.NextSibling
			}
			return current.Parent
		}
		if current.FirstChild != nil {
			return current.FirstChild
		}
		if current.NextSibling != nil {
			return current.NextSibling
		}
		return current.Parent
	}
	node := next(nil, root)
	for node != nil && node != root {
		if node.Type == html.TextNode {
			value := collapseSpaces.ReplaceAllString(node.Data, " ")
			if (prevText == nil || strings.HasSuffix(prevText.Data, " ")) && !keepLeading && strings.HasPrefix(value, " ") {
				value = value[1:]
			}
			if value == "" {
				following := node.NextSibling
				if following == nil {
					following = node.Parent
				}
				node.Parent.RemoveChild(node)
				node = following
				continue
			}
			node.Data = value
			prevText = node
		} else if node.Type == html.ElementNode {
			if nodeBlock(node) || node.Data == "br" {
				if prevText != nil {
					prevText.Data = strings.TrimSuffix(prevText.Data, " ")
				}
				prevText = nil
				keepLeading = false
			} else if voids[node.Data] || node.Data == "pre" {
				prevText = nil
				keepLeading = true
			} else if prevText != nil {
				keepLeading = false
			}
		} else {
			following := node.NextSibling
			if following == nil {
				following = node.Parent
			}
			node.Parent.RemoveChild(node)
			node = following
			continue
		}
		following := next(prev, node)
		prev = node
		node = following
	}
	if prevText != nil {
		prevText.Data = strings.TrimSuffix(prevText.Data, " ")
		if prevText.Data == "" {
			prevText.Parent.RemoveChild(prevText)
		}
	}
}
func jsWhite(r rune) bool {
	return r >= 9 && r <= 13 || r == 32 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}
func trimJS(s string) string { return strings.TrimFunc(s, jsWhite) }
func edgeWhitespace(value string) (leading, trailing string) {
	valueRunes := []rune(value)
	a, b := 0, len(valueRunes)
	for a < b && jsWhite(valueRunes[a]) {
		a++
	}
	for b > a && jsWhite(valueRunes[b-1]) {
		b--
	}
	return string(valueRunes[:a]), string(valueRunes[b:])
}
func flank(node *html.Node) (leading, trailing string) {
	if nodeBlock(node) {
		return "", ""
	}
	leading, trailing = edgeWhitespace(textContent(node))
	left, right := node.PrevSibling, node.NextSibling
	if left != nil && (left.Type == html.TextNode || left.Type == html.ElementNode && !nodeBlock(left)) && strings.HasSuffix(textContent(left), " ") {
		leading = strings.TrimLeft(leading, " \t\r\n")
	}
	if right != nil && (right.Type == html.TextNode || right.Type == html.ElementNode && !nodeBlock(right)) && strings.HasPrefix(textContent(right), " ") {
		trailing = strings.TrimRight(trailing, " \t\r\n")
	}
	return
}
func markdownJoin(left, right string) string {
	l := strings.TrimRight(left, "\n")
	r := strings.TrimLeft(right, "\n")
	count := max(len(left)-len(l), len(right)-len(r))
	return l + strings.Repeat("\n", min(2, count)) + r
}
func attr(node *html.Node, key string) string {
	for _, a := range node.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

var escapeStart = regexp.MustCompile(`^(#{1,6}) `)
var orderedStart = regexp.MustCompile(`^(\d+)\. `)

func escapeMarkdown(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "*", `\*`)
	if strings.HasPrefix(value, "-") {
		value = "\\" + value
	}
	if strings.HasPrefix(value, "+ ") {
		value = "\\" + value
	}
	if strings.HasPrefix(value, "=") {
		value = "\\" + value
	}
	value = escapeStart.ReplaceAllString(value, `\$1 `)
	value = strings.ReplaceAll(value, "`", "\\`")
	if strings.HasPrefix(value, "~~~") {
		value = "\\" + value
	}
	value = strings.ReplaceAll(value, "[", `\[`)
	value = strings.ReplaceAll(value, "]", `\]`)
	if strings.HasPrefix(value, ">") {
		value = "\\" + value
	}
	value = strings.ReplaceAll(value, "_", `\_`)
	return orderedStart.ReplaceAllString(value, `$1\. `)
}

var cleanAttrPattern = regexp.MustCompile(`(?:\n+[\t\r\n \f\v\x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]*)+`)

func cleanAttr(value string) string { return cleanAttrPattern.ReplaceAllString(value, "\n") }
func linkDestination(value string) string {
	value = strings.NewReplacer("<", `\<`, ">", `\>`, "(", `\(`, ")", `\)`).Replace(value)
	if strings.Contains(value, " ") {
		value = "<" + value + ">"
	}
	return value
}
func renderHTML(parent *html.Node, inCode bool) string {
	output := ""
	for child := parent.FirstChild; child != nil; child = child.NextSibling {
		isCode := inCode || child.Data == "code"
		replacement := ""
		if child.Type == html.TextNode {
			replacement = child.Data
			if !isCode {
				replacement = escapeMarkdown(replacement)
			}
		} else if child.Type == html.ElementNode {
			content := renderHTML(child, isCode)
			leading, trailing := flank(child)
			if leading != "" || trailing != "" {
				content = trimJS(content)
			}
			blank := !voids[child.Data] && !meaningful[child.Data] && trimJS(textContent(child)) == "" && !descendant(child, voids) && !descendant(child, meaningful)
			if blank {
				if nodeBlock(child) {
					content = "\n\n"
				} else {
					content = ""
				}
			} else {
				content = htmlRule(child, content)
			}
			replacement = leading + content + trailing
		}
		output = markdownJoin(output, replacement)
	}
	return output
}
func htmlRule(n *html.Node, content string) string {
	switch n.Data {
	case "p":
		return "\n\n" + content + "\n\n"
	case "br":
		return "  \n"
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(n.Data[1] - '0')
		if level < 3 {
			mark := "="
			if level == 2 {
				mark = "-"
			}
			return "\n\n" + content + "\n" + strings.Repeat(mark, len(utf16.Encode([]rune(content)))) + "\n\n"
		}
		return "\n\n" + strings.Repeat("#", level) + " " + content + "\n\n"
	case "blockquote":
		content = strings.Trim(content, "\n")
		return "\n\n> " + strings.ReplaceAll(content, "\n", "\n> ") + "\n\n"
	case "ul", "ol":
		if n.Parent.Data == "li" {
			last := n.Parent.LastChild
			for last != nil && last.Type != html.ElementNode {
				last = last.PrevSibling
			}
			if last == n {
				return "\n" + content
			}
		}
		return "\n\n" + content + "\n\n"
	case "li":
		prefix := "*   "
		if n.Parent.Data == "ol" {
			start := 1.0
			if value := attr(n.Parent, "start"); value != "" {
				start = htmlJSNumber(value)
			}
			index := 0
			for s := n.PrevSibling; s != nil; s = s.PrevSibling {
				if s.Type == html.ElementNode {
					index++
				}
			}
			number := start + float64(index)
			label := fmt.Sprintf("%g", number)
			if math.IsInf(number, 1) {
				label = "Infinity"
			}
			if math.IsInf(number, -1) {
				label = "-Infinity"
			}
			prefix = label + ".  "
		}
		paragraph := strings.HasSuffix(content, "\n")
		content = strings.Trim(content, "\n")
		if paragraph {
			content += "\n"
		}
		content = strings.ReplaceAll(content, "\n", "\n"+strings.Repeat(" ", len(prefix)))
		if n.NextSibling != nil {
			content += "\n"
		}
		return prefix + content
	case "pre":
		if n.FirstChild != nil && n.FirstChild.Data == "code" {
			return "\n\n    " + strings.ReplaceAll(textContent(n.FirstChild), "\n", "\n    ") + "\n\n"
		}
	case "hr":
		return "\n\n* * *\n\n"
	case "a":
		if href := attr(n, "href"); href != "" {
			title := cleanAttr(attr(n, "title"))
			if title != "" {
				title = " \"" + strings.ReplaceAll(title, "\"", "\\\"") + "\""
			}
			return "[" + content + "](" + linkDestination(href) + title + ")"
		}
	case "em", "i":
		if trimJS(content) == "" {
			return ""
		}
		return "_" + content + "_"
	case "strong", "b":
		if trimJS(content) == "" {
			return ""
		}
		return "**" + content + "**"
	case "code":
		if !(n.Parent.Data == "pre" && n.PrevSibling == nil && n.NextSibling == nil) {
			if content == "" {
				return ""
			}
			content = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(content)
			extra := ""
			if strings.HasPrefix(content, "`") || strings.HasSuffix(content, "`") || strings.HasPrefix(content, " ") && strings.HasSuffix(content, " ") && strings.Trim(content, " ") != "" {
				extra = " "
			}
			delimiter := "`"
			runs := map[string]bool{}
			for _, run := range regexp.MustCompile("`+").FindAllString(content, -1) {
				runs[run] = true
			}
			for runs[delimiter] {
				delimiter += "`"
			}
			return delimiter + extra + content + extra + delimiter
		}
	case "img":
		source := attr(n, "src")
		if source == "" {
			return ""
		}
		title := cleanAttr(attr(n, "title"))
		if title != "" {
			title = " \"" + strings.ReplaceAll(title, "\"", "\\\"") + "\""
		}
		return "![" + escapeMarkdown(cleanAttr(attr(n, "alt"))) + "](" + linkDestination(source) + title + ")"
	}
	if nodeBlock(n) {
		return "\n\n" + content + "\n\n"
	}
	return content
}
func HTMLToMarkdown(input string) (string, error) {
	if input == "" {
		return "", nil
	}
	document, err := html.Parse(strings.NewReader("<x-turndown id=\"turndown-root\">" + input + "</x-turndown>"))
	if err != nil {
		return "", err
	}
	var root *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Data == "x-turndown" && attr(n, "id") == "turndown-root" {
			root = n
			return
		}
		for c := n.FirstChild; c != nil && root == nil; c = c.NextSibling {
			find(c)
		}
	}
	find(document)
	if root == nil {
		return "", fmt.Errorf("HTML parser did not preserve Turndown root")
	}
	collapseHTML(root)
	output := renderHTML(root, false)
	return strings.TrimRightFunc(strings.TrimLeft(output, "\t\r\n"), jsWhite), nil
}

func htmlJSNumber(value string) float64 {
	value = trimJS(value)
	if value == "" {
		return 0
	}
	for _, prefix := range []string{"0x", "0X", "0b", "0B", "0o", "0O"} {
		if strings.HasPrefix(value, prefix) {
			base := 16
			if prefix[1] == 'b' || prefix[1] == 'B' {
				base = 2
			}
			if prefix[1] == 'o' || prefix[1] == 'O' {
				base = 8
			}
			number, e := strconv.ParseUint(value[2:], base, 64)
			if e == nil {
				return float64(number)
			}
			return math.NaN()
		}
	}
	if value == "Inf" || value == "+Inf" || value == "-Inf" || value == "infinity" {
		return math.NaN()
	}
	number, e := strconv.ParseFloat(value, 64)
	if e != nil {
		return math.NaN()
	}
	return number
}
