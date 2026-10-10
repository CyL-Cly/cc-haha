package tracecapture

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	weburl "github.com/nlnwa/whatwg-url/url"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func object(v json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = json.Unmarshal(v, &o)
	return o
}
func jstr(s string) json.RawMessage    { return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(s))) }
func units(v json.RawMessage) []uint16 { u, _ := protocol.DecodeJavaScriptJSONString(v); return u }
func text(v json.RawMessage) string    { return string(utf16.Decode(units(v))) }
func length(v json.RawMessage) int     { return len(units(v)) }
func slice(v json.RawMessage, n int) json.RawMessage {
	u := units(v)
	if len(u) > n {
		u = u[:max(0, n)]
	}
	return protocol.QuoteJavaScriptUTF16(u)
}
func obj(fields ...any) protocol.JSONObject {
	o := protocol.NewJSONObject()
	for i := 0; i+1 < len(fields); i += 2 {
		switch v := fields[i+1].(type) {
		case json.RawMessage:
			o.Set(fields[i].(string), v)
		case protocol.JSONObject:
			o.Set(fields[i].(string), raw(v))
		default:
			o.Set(fields[i].(string), raw(v))
		}
	}
	return o
}
func number(v json.RawMessage) float64 {
	var n float64
	if json.Unmarshal(v, &n) != nil || math.IsInf(n, 0) || math.IsNaN(n) {
		return 0
	}
	return n
}
func numeric(v json.RawMessage) bool {
	return len(v) > 0 && (v[0] == '-' || v[0] >= '0' && v[0] <= '9')
}
func truthy(v json.RawMessage) bool {
	v = bytes.TrimSpace(v)
	return len(v) > 0 && !bytes.Equal(v, []byte("null")) && !bytes.Equal(v, []byte("false")) && (!numeric(v) || number(v) != 0) && (v[0] != '"' || length(v) > 0)
}
func array(v json.RawMessage) []json.RawMessage {
	var a []json.RawMessage
	_ = json.Unmarshal(v, &a)
	return a
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func jsSpace(u uint16) bool {
	switch u {
	case 0x0009, 0x000a, 0x000b, 0x000c, 0x000d, 0x0020, 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return u >= 0x2000 && u <= 0x200a
}
func trimUnits(u []uint16) []uint16 {
	for len(u) > 0 && jsSpace(u[0]) {
		u = u[1:]
	}
	for len(u) > 0 && jsSpace(u[len(u)-1]) {
		u = u[:len(u)-1]
	}
	return u
}
func sensitive(k string) bool {
	k = strings.ToLower(k)
	for _, s := range []string{"authorization", "api-key", "api_key", "apikey", "secret", "cookie", "password", "bearer", "session"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	for i := 0; i < len(k); {
		p := strings.Index(k[i:], "token")
		if p < 0 {
			break
		}
		p += i
		if p+5 >= len(k) || k[p+5] != 's' {
			return true
		}
		i = p + 5
	}
	return false
}
func tokenChar(u uint16) bool {
	return u >= 'a' && u <= 'z' || u >= 'A' && u <= 'Z' || u >= '0' && u <= '9' || strings.ContainsRune("._~+/=-", rune(u))
}
func keyChar(u uint16) bool {
	return u >= 'a' && u <= 'z' || u >= 'A' && u <= 'Z' || u >= '0' && u <= '9' || strings.ContainsRune("._-", rune(u))
}
func word(u uint16) bool {
	return u >= 'a' && u <= 'z' || u >= 'A' && u <= 'Z' || u >= '0' && u <= '9' || u == '_'
}
func asciiAt(u []uint16, i int, s string, fold bool) bool {
	if i+len(s) > len(u) {
		return false
	}
	for j, b := range []byte(s) {
		v := u[i+j]
		if fold && v >= 'A' && v <= 'Z' {
			v += 32
		}
		if v != uint16(b) {
			return false
		}
	}
	return true
}
func redactText(v json.RawMessage) json.RawMessage {
	u := units(v)
	if len(u) > CaptureCharLimit {
		return jstr("[trace capture omitted: string-budget]")
	}
	out := make([]uint16, 0, len(u))
	for i := 0; i < len(u); {
		if asciiAt(u, i, "bearer", true) {
			p := i + 6
			q := p
			for q < len(u) && jsSpace(u[q]) {
				q++
			}
			e := q
			for e < len(u) && tokenChar(u[e]) {
				e++
			}
			if q > p && e > q {
				out = append(out, utf16.Encode([]rune("Bearer [redacted]"))...)
				i = e
				continue
			}
		}
		if asciiAt(u, i, "sk-", false) && (i == 0 || !word(u[i-1])) {
			e := i + 3
			for e < len(u) && keyChar(u[e]) {
				e++
			}
			for e > i+3 && !word(u[e-1]) {
				e--
			}
			if e-i-3 >= 8 {
				out = append(out, utf16.Encode([]rune("sk-[redacted]"))...)
				i = e
				continue
			}
		}
		out = append(out, u[i])
		i++
	}
	return protocol.QuoteJavaScriptUTF16(out)
}

type valueBudget struct{ nodes, chars, bytes int }

func (b *valueBudget) count(u []uint16) string {
	b.chars += len(u)
	if b.chars > CaptureCharLimit {
		return "string-budget"
	}
	for _, c := range u {
		switch {
		case c < 0x20:
			b.bytes += 6
		case c == '"' || c == '\\':
			b.bytes += 2
		case c < 0x80:
			b.bytes++
		case c < 0x800:
			b.bytes += 2
		case c >= 0xd800 && c <= 0xdfff:
			b.bytes += 6
		default:
			b.bytes += 3
		}
		if b.bytes > RecordBytesLimit-128*1024 {
			return "byte-budget"
		}
	}
	return ""
}
func (b *valueBudget) visit(v json.RawMessage, d int) (json.RawMessage, string) {
	b.nodes++
	b.bytes += 16
	if b.nodes > CaptureNodeLimit {
		return nil, "node-budget"
	}
	if d > CaptureDepthLimit {
		return nil, "depth-budget"
	}
	v = bytes.TrimSpace(v)
	if len(v) == 0 {
		return json.RawMessage("null"), ""
	}
	switch v[0] {
	case '"':
		if r := b.count(units(v)); r != "" {
			return nil, r
		}
		return v, ""
	case '[':
		a := array(v)
		if len(a) > CaptureNodeLimit-b.nodes {
			return nil, "node-budget"
		}
		for i, e := range a {
			var r string
			a[i], r = b.visit(e, d+1)
			if r != "" {
				return nil, r
			}
		}
		return raw(a), ""
	case '{':
		o := object(v)
		out := protocol.NewJSONObject()
		for _, p := range o.OwnProperties() {
			if b.nodes >= CaptureNodeLimit {
				return nil, "node-budget"
			}
			if r := b.count(units(p.NameJSON)); r != "" {
				return nil, r
			}
			e, r := b.visit(p.Value, d+1)
			if r != "" {
				return nil, r
			}
			_ = out.SetPropertyJSON(p.NameJSON, e)
		}
		return raw(out), ""
	}
	return v, ""
}
func boundedValue(v json.RawMessage) (json.RawMessage, string) {
	b := valueBudget{}
	v, r := b.visit(v, 0)
	if r != "" {
		v = raw(obj("traceCaptureOmitted", obj("reason", r, "nodeLimit", CaptureNodeLimit, "depthLimit", CaptureDepthLimit, "charLimit", CaptureCharLimit)))
	}
	return v, r
}
func redactValue(v json.RawMessage, k string) json.RawMessage {
	if sensitive(k) {
		return jstr("[redacted]")
	}
	v = bytes.TrimSpace(v)
	if len(v) == 0 {
		return json.RawMessage("null")
	}
	switch v[0] {
	case '"':
		return redactText(v)
	case '[':
		a := array(v)
		for i, e := range a {
			a[i] = redactValue(e, "")
		}
		return raw(a)
	case '{':
		o := object(v)
		out := protocol.NewJSONObject()
		for _, p := range o.OwnProperties() {
			_ = out.SetPropertyJSON(p.NameJSON, redactValue(p.Value, text(p.NameJSON)))
		}
		return raw(out)
	}
	return v
}
func pretty(v json.RawMessage) []byte {
	v = protocol.StringifyValidatedJavaScriptJSON(v)
	var b bytes.Buffer
	_ = json.Indent(&b, v, "", "  ")
	return b.Bytes()
}
func parseText(v json.RawMessage) json.RawMessage {
	u := trimUnits(units(v))
	if len(u) == 0 || (u[0] != '{' && u[0] != '[') {
		return v
	}
	s := []byte(protocol.JavaScriptJSONSourceUTF16(u))
	if json.Valid(s) {
		return s
	}
	return v
}
func CreateBodySnapshot(v json.RawMessage, maxPreview int, alreadyTruncated bool) BodySnapshot {
	if maxPreview < 0 {
		maxPreview = PreviewChars
	}
	v, omitted := boundedValue(v)
	v = bytes.TrimSpace(v)
	content := "json"
	serialized := []uint16{}
	if bytes.Equal(v, []byte("null")) || len(v) == 0 {
		content = "empty"
	} else if v[0] == '"' {
		p := parseText(v)
		if p[0] == '"' {
			content = "text"
			serialized = units(redactText(v))
		} else {
			p, _ = boundedValue(p)
			serialized = utf16.Encode([]rune(string(pretty(redactValue(p, "")))))
		}
	} else {
		v, _ = boundedValue(v)
		serialized = utf16.Encode([]rune(string(pretty(redactValue(v, "")))))
	}
	encoded := []byte(string(utf16.Decode(serialized)))
	preview := serialized
	if len(preview) > maxPreview {
		preview = preview[:maxPreview]
	}
	return BodySnapshot{ContentType: content, Bytes: int64(len(encoded)), SHA256: digest(encoded), Preview: protocol.QuoteJavaScriptUTF16(preview), Truncated: omitted != "" || alreadyTruncated || len(serialized) > maxPreview, CaptureOmitted: omitted}
}
func boundedSnapshot(v BodySnapshot) BodySnapshot {
	v.Truncated = v.Truncated || length(v.Preview) > PreviewChars
	v.Preview = slice(v.Preview, PreviewChars)
	v.SHA256 = text(slice(jstr(v.SHA256), 128))
	v.CaptureOmitted = text(slice(jstr(v.CaptureOmitted), 128))
	return v
}
func EmptyBody(bytes int64) BodySnapshot {
	return BodySnapshot{ContentType: "empty", Bytes: bytes, SHA256: "", Preview: jstr(""), Truncated: bytes > 0}
}
func sanitizeHeaders(headers http.Header) protocol.JSONObject {
	o := protocol.NewJSONObject()
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, strings.ToLower(k))
	}
	sort.Strings(keys)
	for _, k := range keys {
		var vals []string
		for name, v := range headers {
			if strings.EqualFold(k, name) {
				vals = append(vals, v...)
			}
		}
		v := jstr(strings.Join(vals, ", "))
		if sensitive(k) {
			v = jstr("[redacted]")
		} else {
			v = redactText(v)
		}
		o.Set(k, v)
	}
	return o
}
func formEncode(s string) string {
	var b strings.Builder
	const h = "0123456789ABCDEF"
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '*' || c == '-' || c == '.' || c == '_' {
			b.WriteByte(c)
		} else if c == ' ' {
			b.WriteByte('+')
		} else {
			b.WriteByte('%')
			b.WriteByte(h[c>>4])
			b.WriteByte(h[c&15])
		}
	}
	return b.String()
}
func sanitizeURL(s string) string {
	if len(utf16.Encode([]rune(s))) > 8192 {
		return "[trace URL omitted: string-budget]"
	}
	u, e := weburl.Parse(s)
	if e != nil {
		return s
	}
	original := u.Href(false)
	params := u.SearchParams()
	keys := []string{}
	params.Iterate(func(pair *weburl.NameValuePair) { keys = append(keys, pair.Name) })
	changed := false
	for _, k := range keys {
		if sensitive(k) {
			params.Set(k, "[redacted]")
			changed = true
		}
	}
	if !changed {
		return original
	}
	pairs := []string{}
	params.Iterate(func(pair *weburl.NameValuePair) {
		pairs = append(pairs, formEncode(pair.Name)+"="+formEncode(pair.Value))
	})
	u.SetSearch(strings.Join(pairs, "&"))
	return u.Href(false)
}

func semantic(v json.RawMessage, source string) json.RawMessage {
	v, r := boundedValue(v)
	if r != "" {
		return raw(obj("version", 1, "request", v))
	}
	v = bytes.TrimSpace(v)
	if len(v) > 0 && v[0] == '"' {
		u := trimUnits(units(v))
		if len(u) == 0 || u[0] != '{' {
			return nil
		}
		v = []byte(protocol.JavaScriptJSONSourceUTF16(u))
		if !json.Valid(v) {
			return nil
		}
	}
	o := object(v)
	if !o.IsObject() {
		return nil
	}
	if source == "proxy" {
		a := object(o.Get("anthropic"))
		if a.IsObject() {
			o = a
		}
	}
	messages := bytes.TrimSpace(o.Get("messages"))
	input := bytes.TrimSpace(o.Get("input"))
	if !(len(messages) > 0 && messages[0] == '[') && !(len(input) > 0 && input[0] == '[') && o.Get("system") == nil && o.Get("instructions") == nil {
		return nil
	}
	v, _ = boundedValue(raw(o))
	return raw(obj("version", 1, "request", compactSemantic(v, "")))
}
func compactSemantic(v json.RawMessage, k string) json.RawMessage {
	if sensitive(k) {
		return jstr("[redacted]")
	}
	v = bytes.TrimSpace(v)
	if len(v) == 0 {
		return json.RawMessage("null")
	}
	if v[0] == '[' {
		a := array(v)
		for i, e := range a {
			a[i] = compactSemantic(e, "")
		}
		return raw(a)
	}
	if v[0] != '{' {
		if v[0] == '"' {
			return redactText(v)
		}
		return v
	}
	o := object(v)
	out := protocol.NewJSONObject()
	imageSource := object(o.Get("source"))
	image := text(o.Get("type")) == "image" && text(imageSource.Get("type")) == "base64" && len(imageSource.Get("data")) > 0 && imageSource.Get("data")[0] == '"'
	for _, p := range o.OwnProperties() {
		k := text(p.NameJSON)
		if image && k == "source" {
			continue
		}
		_ = out.SetPropertyJSON(p.NameJSON, compactSemantic(p.Value, k))
	}
	if image {
		data := text(imageSource.Get("data"))
		clean := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '+' || r == '/' || r == '=' || r == '-' || r == '_' {
				return r
			}
			return -1
		}, data)
		clean = strings.NewReplacer("-", "+", "_", "/").Replace(clean)
		clean = strings.TrimRight(clean, "=")
		decoded, _ := base64.RawStdEncoding.DecodeString(clean)
		s := obj("type", "base64")
		if media := imageSource.Get("media_type"); len(media) > 0 && media[0] == '"' {
			s.Set("media_type", media)
		}
		s.Set("bytes", raw(len(decoded)))
		s.Set("sha256", jstr(digest(decoded)))
		out.Set("source", raw(s))
	}
	return raw(out)
}
func defaultNumber(o protocol.JSONObject, k string, f float64) float64 {
	if o.Get(k) == nil {
		return f
	}
	return number(o.Get(k))
}
func nullableString(v json.RawMessage) *string {
	if len(v) == 0 || bytes.Equal(v, []byte("null")) {
		return nil
	}
	s := text(v)
	return &s
}
func nullableNumber(v json.RawMessage) *float64 {
	if !numeric(v) {
		return nil
	}
	n := number(v)
	return &n
}
func integerString(i int64) string { return strconv.FormatInt(i, 10) }

func decodeLooseBase64(s string) []byte {
	clean := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '+' || r == '/' || r == '=' || r == '-' || r == '_' {
			return r
		}
		return -1
	}, s)
	clean = strings.NewReplacer("-", "+", "_", "/").Replace(clean)
	if n := strings.IndexByte(clean, '='); n >= 0 {
		clean = clean[:n]
	}
	b, _ := base64.RawStdEncoding.DecodeString(clean)
	return b
}
