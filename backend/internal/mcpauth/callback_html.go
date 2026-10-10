package mcpauth

import (
	"io"
	"strings"

	"golang.org/x/net/html"
)

// Callback text uses the original XSS whitelist instead of reflecting a raw
// OAuth provider message into the locally served consent completion page.
var callbackTags = func() map[string]map[string]bool {
	result := map[string]map[string]bool{}
	for _, tag := range strings.Fields("address article aside b big blockquote br caption center cite code dd del details div dl dt em figcaption figure footer h1 h2 h3 h4 h5 h6 header hr i ins kbd li mark nav ol p pre s section small span sub summary sup strong strike table tbody td tfoot th thead tr tt u ul") {
		result[tag] = map[string]bool{}
	}
	attrs := map[string][]string{"a": {"target", "href", "title"}, "abbr": {"title"}, "area": {"shape", "coords", "href", "alt"}, "audio": {"autoplay", "controls", "crossorigin", "loop", "muted", "preload", "src"}, "bdi": {"dir"}, "bdo": {"dir"}, "blockquote": {"cite"}, "col": {"align", "valign", "span", "width"}, "colgroup": {"align", "valign", "span", "width"}, "del": {"datetime"}, "details": {"open"}, "font": {"color", "size", "face"}, "img": {"src", "alt", "title", "width", "height", "loading"}, "ins": {"datetime"}, "table": {"width", "border", "align", "valign"}, "tbody": {"align", "valign"}, "td": {"width", "rowspan", "colspan", "align", "valign"}, "tfoot": {"align", "valign"}, "th": {"width", "rowspan", "colspan", "align", "valign"}, "thead": {"align", "valign"}, "tr": {"rowspan", "align", "valign"}, "video": {"autoplay", "controls", "crossorigin", "loop", "muted", "playsinline", "poster", "preload", "src", "height", "width"}}
	for tag, names := range attrs {
		allowed := map[string]bool{}
		for _, name := range names {
			allowed[name] = true
		}
		result[tag] = allowed
	}
	return result
}()

func escapeCallback(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "<", "&lt;"), ">", "&gt;")
}
func sanitizeCallbackHTML(value string) string {
	parser := html.NewTokenizer(strings.NewReader(value))
	var result strings.Builder
	for {
		kind := parser.Next()
		if kind == html.ErrorToken {
			if parser.Err() == io.EOF {
				return result.String()
			}
			return escapeCallback(value)
		}
		raw := string(parser.Raw())
		switch kind {
		case html.TextToken:
			result.WriteString(escapeCallback(raw))
		case html.CommentToken:
			continue
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			token := parser.Token()
			allowed, exists := callbackTags[token.Data]
			if !exists {
				result.WriteString(escapeCallback(raw))
				continue
			}
			if kind == html.EndTagToken {
				result.WriteString("</" + token.Data + ">")
				continue
			}
			result.WriteString("<" + token.Data)
			for _, attribute := range token.Attr {
				if !allowed[attribute.Key] {
					continue
				}
				value := attribute.Val
				if attribute.Key == "href" || attribute.Key == "src" {
					lower := strings.ToLower(strings.TrimSpace(value))
					safe := lower == "" || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "mailto:") || strings.HasPrefix(lower, "tel:") || strings.HasPrefix(lower, "ftp://") || strings.HasPrefix(lower, "#") || strings.HasPrefix(lower, "/") || strings.HasPrefix(lower, "./") || strings.HasPrefix(lower, "../")
					if attribute.Key == "src" && strings.HasPrefix(lower, "data:image/") {
						safe = true
					}
					if !safe {
						value = ""
					}
				}
				result.WriteByte(' ')
				result.WriteString(attribute.Key)
				if value != "" {
					result.WriteString("=\"")
					result.WriteString(strings.ReplaceAll(escapeCallback(value), "\"", "&quot;"))
					result.WriteByte('"')
				}
			}
			if kind == html.SelfClosingTagToken {
				result.WriteString(" />")
			} else {
				result.WriteByte('>')
			}
		default:
			result.WriteString(escapeCallback(raw))
		}
	}
}
