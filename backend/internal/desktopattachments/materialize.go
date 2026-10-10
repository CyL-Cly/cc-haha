// Package desktopattachments materializes the desktop transport's original
// attachment references before admission to the shared structured SDK input.
package desktopattachments

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Options struct {
	HomeDir, ConfigDir string
	NewID              func() string
	ImageProcessor     func([]byte, int, string) (imageprocessing.Result, error)
}

var ErrImageProducer = errors.New("desktop image attachment normalization producer is not bound")
var dataURL = regexp.MustCompile(`^data:([^;,]+)?;base64,([^\r\n]*)$`)
var extension = regexp.MustCompile(`(?i)\.([a-z0-9]+)$`)
var imageExtension = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp)$`)

func Build(content, sessionID string, attachments json.RawMessage, o Options) (json.RawMessage, error) {
	if !filepath.IsAbs(o.HomeDir) || o.NewID == nil {
		return nil, errors.New("desktop attachments require explicit HOME and UUID producer")
	}
	var refs []protocol.JSONObject
	if len(attachments) > 0 && string(attachments) != "null" {
		if err := json.Unmarshal(attachments, &refs); err != nil {
			return nil, err
		}
	}
	// Fail before staging any files when the native image boundary is unavailable.
	for _, ref := range refs {
		if inlineImage(ref) && o.ImageProcessor == nil {
			return nil, ErrImageProducer
		}
	}
	config := o.ConfigDir
	if config == "" {
		config = filepath.Join(o.HomeDir, ".claude")
	}
	uploadDir := filepath.Join(config, "uploads", sessionID)
	paths := []string{}
	images := []protocol.JSONObject{}
	metadata := []string{}
	for _, ref := range refs {
		if inlineImage(ref) {
			block, text, ok := materializeImage(ref, uploadDir, o)
			if ok {
				images = append(images, block)
				if text != "" {
					metadata = append(metadata, text)
				}
				continue
			}
		}
		if path := ref.String("path"); path != "" {
			paths = append(paths, path)
			continue
		}
		data := ref.String("data")
		if data == "" {
			continue
		}
		mime := ref.String("mimeType")
		encoded := data
		if match := dataURL.FindStringSubmatch(data); match != nil {
			encoded = match[2]
			if len(ref.Get("mimeType")) == 0 || string(ref.Get("mimeType")) == "null" {
				mime = match[1]
			}
		}
		ext := attachmentExtension(ref.String("name"), ref.String("path"), mime, ref.String("type"))
		name := sanitizeName(ref.String("name"), ref.String("type"), ext)
		if err := os.MkdirAll(uploadDir, 0755); err != nil {
			return nil, err
		}
		path := filepath.Join(uploadDir, o.NewID()+"-"+name)
		if err := os.WriteFile(path, decodeNodeBase64(encoded), 0666); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	text := strings.TrimFunc(content, jsWhitespace)
	if len(paths) > 0 {
		if text == "" {
			text = "Please analyze the attached files."
		}
		quoted := make([]string, len(paths))
		for i, path := range paths {
			quoted[i] = `@"` + path + `"`
		}
		text = strings.TrimFunc(strings.Join(quoted, " ")+" "+text, jsWhitespace)
	}
	blocks := []protocol.JSONObject{}
	if text != "" {
		blocks = append(blocks, imageText(text))
	} else if len(images) > 0 {
		blocks = append(blocks, imageText("Please analyze the attached image."))
	}
	blocks = append(blocks, images...)
	for _, text := range metadata {
		blocks = append(blocks, imageText(text))
	}
	if len(blocks) == 0 {
		blocks = append(blocks, imageText(""))
	}
	return json.Marshal(blocks)
}
func imageText(text string) protocol.JSONObject {
	raw, _ := json.Marshal(text)
	return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"text"`)}, protocol.JSONField{Name: "text", Value: raw})
}
func materializeImage(ref protocol.JSONObject, directory string, o Options) (protocol.JSONObject, string, bool) {
	var data []byte
	mime := ref.String("mimeType")
	if value := ref.String("data"); value != "" {
		encoded := value
		if match := dataURL.FindStringSubmatch(value); match != nil {
			encoded = match[2]
			if ref.Get("mimeType") == nil || string(ref.Get("mimeType")) == "null" {
				mime = match[1]
			}
		}
		data = decodeNodeBase64(encoded)
	} else if path := ref.String("path"); path != "" && string(ref.Get("isDirectory")) != "true" {
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			return protocol.JSONObject{}, "", false
		}
	} else {
		return protocol.JSONObject{}, "", false
	}
	ext := attachmentExtension(ref.String("name"), ref.String("path"), mime, ref.String("type"))
	processed, err := o.ImageProcessor(data, len(data), ext)
	if err != nil {
		return protocol.JSONObject{}, "", false
	}
	ext = strings.ToLower(strings.Split(strings.Split(processed.MediaType, "/")[len(strings.Split(processed.MediaType, "/"))-1], "+")[0])
	if ext == "" {
		ext = "png"
	}
	if ext == "jpg" {
		ext = "jpeg"
	}
	name := sanitizeName(ref.String("name"), ref.String("type"), ext)
	if match := extension.FindStringIndex(name); match != nil {
		name = name[:match[0]] + "." + ext
	} else {
		name += "." + ext
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		return protocol.JSONObject{}, "", false
	}
	path := filepath.Join(directory, o.NewID()+"-"+name)
	if err := os.WriteFile(path, processed.Buffer, 0666); err != nil {
		return protocol.JSONObject{}, "", false
	}
	sourceRaw, _ := json.Marshal(map[string]string{"type": "base64", "media_type": "image/" + ext, "data": base64.StdEncoding.EncodeToString(processed.Buffer)})
	block := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"image"`)}, protocol.JSONField{Name: "source", Value: sourceRaw})
	return block, imageprocessing.MetadataText(processed.Dimensions, path), true
}

func inlineImage(ref protocol.JSONObject) bool {
	if string(ref.Get("isDirectory")) == "true" {
		return false
	}
	if ref.String("type") == "image" || strings.HasPrefix(ref.String("mimeType"), "image/") {
		return true
	}
	candidate := ref.String("path")
	if len(ref.Get("path")) == 0 || string(ref.Get("path")) == "null" {
		candidate = ref.String("name")
	}
	return imageExtension.MatchString(candidate)
}
func attachmentExtension(name, path, mime, kind string) string {
	for _, candidate := range []string{name, path} {
		if match := extension.FindStringSubmatch(candidate); match != nil {
			return match[1]
		}
	}
	if parts := strings.Split(mime, "/"); len(parts) > 1 {
		if value := strings.Split(parts[1], "+")[0]; value != "" {
			return value
		}
	}
	if kind == "image" {
		return "png"
	}
	return "bin"
}
func sanitizeName(name, kind, ext string) string {
	if kind == "" {
		kind = "undefined"
	}
	fallback := kind + "-attachment." + ext
	if name == "" {
		name = fallback
	}
	var normalized strings.Builder
	for _, unit := range utf16.Encode([]rune(name)) {
		if unit >= 'a' && unit <= 'z' || unit >= 'A' && unit <= 'Z' || unit >= '0' && unit <= '9' || unit == '.' || unit == '_' || unit == '-' {
			normalized.WriteByte(byte(unit))
		} else {
			normalized.WriteByte('_')
		}
	}
	if normalized.Len() == 0 {
		return fallback
	}
	return normalized.String()
}
func decodeNodeBase64(value string) []byte {
	var encoded strings.Builder
	for _, r := range value {
		if r == '=' {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '+', r == '/':
			encoded.WriteByte(byte(r))
		case r == '-':
			encoded.WriteByte('+')
		case r == '_':
			encoded.WriteByte('/')
		}
	}
	data := encoded.String()
	if len(data)%4 == 1 {
		data = data[:len(data)-1]
	}
	decoded, _ := base64.RawStdEncoding.DecodeString(data)
	return decoded
}
func jsWhitespace(r rune) bool {
	return r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == ' ' || r == '\u00a0' || r == '\u1680' || r >= '\u2000' && r <= '\u200a' || r == '\u2028' || r == '\u2029' || r == '\u202f' || r == '\u205f' || r == '\u3000' || r == '\ufeff'
}
