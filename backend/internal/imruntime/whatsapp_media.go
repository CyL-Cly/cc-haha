package imruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"path/filepath"
	"strings"
)

type WhatsAppMediaDescriptor struct {
	MediaType, MIME, Name, Kind string
	Content, Media              protocol.JSONObject
}

func WhatsAppDescribeMedia(content json.RawMessage) (*WhatsAppMediaDescriptor, error) {
	normalized := NormalizeWhatsAppContent(content)
	o := object(normalized)
	var first string
	for _, name := range []string{"imageMessage", "videoMessage", "documentMessage", "audioMessage", "stickerMessage"} {
		if truthy(o.Get(name)) {
			first = name
			break
		}
	}
	if first == "" {
		return nil, nil
	}
	var mime json.RawMessage
	for _, name := range []string{"imageMessage", "videoMessage", "documentMessage", "audioMessage", "stickerMessage"} {
		if v := object(o.Get(name)).Get("mimetype"); len(v) > 0 && string(v) != "null" {
			mime = v
			break
		}
	}
	if len(mime) == 0 {
		for _, pair := range [][2]string{{"audioMessage", "audio/ogg; codecs=opus"}, {"imageMessage", "image/jpeg"}, {"videoMessage", "video/mp4"}, {"stickerMessage", "image/webp"}} {
			if truthy(o.Get(pair[0])) {
				mime = raw(pair[1])
				break
			}
		}
	}
	if len(mime) == 0 {
		mime = raw("application/octet-stream")
	}
	if mime[0] != '"' {
		return nil, errors.New("mimeType.startsWith is not a function")
	}
	mimeText := TextJSON(mime).String()
	name := object(o.Get("documentMessage")).Get("fileName")
	nameText := ""
	if truthy(name) {
		if name[0] != '"' {
			return nil, errors.New("explicit.trim is not a function")
		}
		if len(TextJSON(name).Trim()) > 0 {
			nameText = filepath.Base(TextJSON(name).String())
		}
	}
	if nameText == "" {
		base, fallback := "file", "bin"
		switch first {
		case "imageMessage":
			base, fallback = "image", "jpg"
		case "videoMessage":
			base, fallback = "video", "mp4"
		case "audioMessage":
			base, fallback = "audio", "ogg"
		case "stickerMessage":
			base, fallback = "sticker", "webp"
		}
		nameText = "whatsapp-" + base + "." + whatsAppExtension(mimeText, fallback)
	}
	kind := "file"
	if strings.HasPrefix(mimeText, "image/") {
		kind = "image"
	}
	return &WhatsAppMediaDescriptor{strings.TrimSuffix(first, "Message"), mimeText, nameText, kind, o, object(o.Get(first))}, nil
}
func whatsAppExtension(mime, fallback string) string {
	m := strings.ToLower(protocol.TrimJavaScriptString(strings.SplitN(mime, ";", 2)[0]))
	if ext := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/gif": "gif", "image/webp": "webp", "video/mp4": "mp4", "audio/ogg": "ogg", "audio/mpeg": "mp3", "application/pdf": "pdf"}[m]; ext != "" {
		return ext
	}
	return fallback
}
func WhatsAppMediaURL(media protocol.JSONObject) string {
	url := media.Get("url")
	if len(url) > 0 && url[0] == '"' && strings.HasPrefix(TextJSON(url).String(), "https://mmg.whatsapp.net/") {
		return TextJSON(url).String()
	}
	direct, e := protocol.JavaScriptStringJSON(media.Get("directPath"))
	if e != nil {
		return "https://mmg.whatsapp.netundefined"
	}
	return "https://mmg.whatsapp.net" + TextJSON(direct).String()
}

type WhatsAppMediaHTTPError struct {
	URL        string
	StatusCode int
}

func (e *WhatsAppMediaHTTPError) Error() string { return "Failed to fetch stream from " + e.URL }

// WhatsAppMediaFetchStatusError represents a low-level fetch rejection with
// a numeric status, rather than an ordinary non-OK HTTP response.
type WhatsAppMediaFetchStatusError struct {
	Status int
	Cause  error
}

func (e *WhatsAppMediaFetchStatusError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "media fetch failed"
}
func (e *WhatsAppMediaFetchStatusError) Unwrap() error            { return e.Cause }
func (e *WhatsAppMediaFetchStatusError) WhatsAppMediaStatus() int { return e.Status }

type WhatsAppMedia struct {
	Store    *AttachmentStore
	HTTP     *http.Client
	Reupload func(context.Context, protocol.JSONObject) (protocol.JSONObject, error)
}

func (m *WhatsAppMedia) Download(ctx context.Context, message protocol.JSONObject, session string) (*LocalAttachment, error) {
	local, e := m.downloadOnce(ctx, message, session)
	// The installed SDK tests error.status, not Boom.output.statusCode.
	// A normal non-OK HTTP response therefore does not request re-upload.
	var statusError interface{ WhatsAppMediaStatus() int }
	if m.Reupload != nil && errors.As(e, &statusError) && (statusError.WhatsAppMediaStatus() == 404 || statusError.WhatsAppMediaStatus() == 410) {
		updated, e := m.Reupload(ctx, message)
		if e != nil {
			return nil, e
		}
		return m.downloadOnce(ctx, updated, session)
	}
	return local, e
}
func (m *WhatsAppMedia) downloadOnce(ctx context.Context, message protocol.JSONObject, session string) (*LocalAttachment, error) {
	d, e := WhatsAppDescribeMedia(message.Get("message"))
	if e != nil || d == nil {
		return nil, e
	}
	if m.Store == nil || m.HTTP == nil {
		return nil, errors.New("WhatsApp media requires shared attachment store and HTTP transport")
	}
	// The SDK's extractMessageContent/getContentType uses own key order, rather
	// than the descriptor's MIME priority. Preserve this distinction.
	content := whatsAppExtractMediaContent(d.Content)
	mediaType := ""
	var entry protocol.JSONObject
	for _, f := range content.OwnProperties() {
		name := TextJSON(f.NameJSON).String()
		if name == "conversation" || strings.Contains(name, "Message") && name != "senderKeyDistributionMessage" {
			mediaType = strings.TrimSuffix(name, "Message")
			entry = object(f.Value)
			break
		}
	}
	if !entry.IsObject() || len(entry.Get("url")) == 0 && len(entry.Get("thumbnailDirectPath")) == 0 {
		return nil, fmt.Errorf("\"%sMessage\" message is not a media message", mediaType)
	}
	if len(entry.Get("thumbnailDirectPath")) > 0 && len(entry.Get("url")) == 0 {
		entry = protocol.NewJSONObject(protocol.JSONField{Name: "directPath", Value: entry.Get("thumbnailDirectPath")}, protocol.JSONField{Name: "mediaKey", Value: entry.Get("mediaKey")})
		mediaType = "thumbnail-link"
	}
	key, e := WhatsAppMediaKey(entry.Get("mediaKey"))
	if e != nil {
		return nil, e
	}
	keys, e := WhatsAppDeriveMediaKeys(key, mediaType)
	if e != nil {
		return nil, e
	}
	url := WhatsAppMediaURL(entry)
	req, e := http.NewRequestWithContext(ctx, "GET", url, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Origin", "https://web.whatsapp.com")
	res, e := m.HTTP.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, &WhatsAppMediaHTTPError{url, res.StatusCode}
	}
	encrypted, e := io.ReadAll(res.Body)
	if e != nil {
		return nil, e
	}
	data, e := DecryptWhatsAppMedia(encrypted, keys)
	if e != nil {
		return nil, e
	}
	target, e := m.Store.ResolvePath("whatsapp", session, d.Name)
	if e != nil {
		return nil, e
	}
	if e = m.Store.Write(target, data); e != nil {
		return nil, e
	}
	return &LocalAttachment{Kind: d.Kind, Name: d.Name, Path: target, Size: int64(len(data)), MIME: d.MIME, Buffer: data}, nil
}
func whatsAppExtractMediaContent(o protocol.JSONObject) protocol.JSONObject {
	var template protocol.JSONObject
	if truthy(o.Get("buttonsMessage")) {
		template = object(o.Get("buttonsMessage"))
	} else {
		outer := object(o.Get("templateMessage"))
		if truthy(outer.Get("hydratedFourRowTemplate")) {
			template = object(outer.Get("hydratedFourRowTemplate"))
		} else if truthy(outer.Get("hydratedTemplate")) {
			template = object(outer.Get("hydratedTemplate"))
		}
	}
	if !template.IsObject() {
		return o
	}
	for _, name := range []string{"imageMessage", "documentMessage", "videoMessage", "locationMessage"} {
		if truthy(template.Get(name)) {
			return protocol.NewJSONObject(protocol.JSONField{Name: name, Value: template.Get(name)})
		}
	}
	text := template.Get("contentText")
	if len(text) == 0 {
		text = template.Get("hydratedContentText")
	}
	if len(text) == 0 {
		text = raw("")
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "conversation", Value: text})
}

// WhatsAppOutboundMedia is the original ChatPort payload before the socket's
// encrypted send. The actual socket producer must consume the Buffer fields.
func WhatsAppOutboundMedia(data []byte, mime string, caption, fileName json.RawMessage) protocol.JSONObject {
	field := "document"
	if strings.HasPrefix(mime, "image/") {
		field = "image"
	} else if strings.HasPrefix(mime, "video/") {
		field = "video"
	} else if strings.HasPrefix(mime, "audio/") {
		field = "audio"
	}
	o := protocol.NewJSONObject(protocol.JSONField{Name: field, Value: whatsAppBuffer(data)})
	if field == "audio" {
		o.Set("ptt", raw(true))
		if mime == "audio/ogg" {
			mime = "audio/ogg; codecs=opus"
		}
	} else {
		if field == "document" {
			if !truthy(fileName) {
				fileName = raw("file")
			}
			o.Set("fileName", fileName)
		}
		o.Set("caption", caption)
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	o.Set("mimetype", raw(mime))
	return o
}

func whatsAppRetryMedia(content json.RawMessage) (string, protocol.JSONObject, error) {
	o := whatsAppExtractMediaContent(object(NormalizeWhatsAppContent(content)))
	for _, name := range []string{"documentMessage", "imageMessage", "videoMessage", "audioMessage", "stickerMessage"} {
		if truthy(o.Get(name)) {
			return name, object(o.Get(name)), nil
		}
	}
	return "", protocol.JSONObject{}, errors.New("given message is not a media message")
}
func whatsAppSetRetryMediaPath(content json.RawMessage, field, direct string, depth int) json.RawMessage {
	if depth > 7 {
		return content
	}
	o := object(content)
	if !o.IsObject() {
		return content
	}

	for _, name := range []string{"ephemeralMessage", "viewOnceMessage", "documentWithCaptionMessage", "viewOnceMessageV2", "viewOnceMessageV2Extension", "editedMessage"} {
		if truthy(o.Get(name)) {
			wrapper := object(o.Get(name))
			wrapper.Set("message", whatsAppSetRetryMediaPath(wrapper.Get("message"), field, direct, depth+1))
			o.Set(name, raw(wrapper))
			return raw(o)
		}
	}
	for _, name := range []string{"buttonsMessage", "templateMessage", "hydratedFourRowTemplate", "hydratedTemplate"} {
		if truthy(o.Get(name)) {
			o.Set(name, whatsAppSetRetryMediaPath(o.Get(name), field, direct, depth+1))
			return raw(o)
		}
	}
	if truthy(o.Get(field)) {
		media := object(o.Get(field))
		media.Set("directPath", raw(direct))
		media.Set("url", raw("https://mmg.whatsapp.net"+direct))
		o.Set(field, raw(media))
		return raw(o)
	}
	return content
}
