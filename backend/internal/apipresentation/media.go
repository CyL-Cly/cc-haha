package apipresentation

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// MessagesForAPI is the actual prepared history for the failed attempt, not
// canonical history or a caller-supplied assertion that images were present.
type MediaOptions struct {
	Model          string
	NonInteractive bool
	MessagesForAPI []protocol.JSONObject
}

var mediaPDFPages = regexp.MustCompile(`maximum of [0-9]+ PDF pages`)
var mediaOverflow = regexp.MustCompile(`(?i)prompt is too long|input is too long for requested model|context_length_exceeded|maximum context length|exceeds? the context window|context window exceeded|supports only [0-9]+\s*k?\s*(tokens?\s+of\s+)?context`)

// NativeMediaFailure selects only the original media presentation branches.
// Administrative failures never enter this mapper. A false match preserves
// the existing distinct auth, capacity, compaction and unknown-error guards.
func NativeMediaFailure(failure error, domain Domain, factory *messages.Factory, input MediaOptions) (*streamhistory.Envelope, bool, error) {
	if !domain.direct || factory == nil || absent(failure) {
		return nil, false, unsupported("native media error domain/factory")
	}
	if wrapped, ok := failure.(*retrypolicy.CannotRetryError); ok {
		failure = wrapped.Original
	}
	if absent(failure) {
		return nil, false, nil
	}
	selected := storage.Object{}
	content := func(code, text string) {
		selected["content"] = quote(text)
		selected["businessErrorCode"] = quote(code)
	}
	switch failure.(type) {
	case *requestcontext.ImageSizeError:
		content("image_too_large", mediaImageTooLarge(input.NonInteractive))
	case *imageprocessing.ResizeError:
		content("image_too_large", mediaImageTooLarge(input.NonInteractive))
	default:
		raw, api, status, represented := mediaOriginal(failure)
		if !represented {
			return nil, false, nil
		}
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		if err != nil {
			return nil, false, err
		}
		text := string(utf16.Decode(units))
		if unsupportedImageText(text) && (!api || status != nil && (*status == 400 || *status == 422)) {
			content("image_unsupported", mediaImageUnsupported(input.NonInteractive))
			selected["error"] = quote("invalid_request")
			selected["errorDetails"] = raw
			selected["sourceModel"] = quote(input.Model)
		} else {
			// These Source branches precede media classifiers and need their actual
			// independent producers; do not mistake their faults for image rejection.
			if strings.Contains(text, "Opus is experiencing high load, please use /model to switch to Sonnet") || api && status != nil && *status == 429 || mediaOverflow.MatchString(text) {
				return nil, false, nil
			}
			switch {
			case mediaPDFPages.MatchString(text):
				content("pdf_too_large", mediaPDFTooLarge(input.NonInteractive))
				selected["error"] = quote("invalid_request")
				selected["errorDetails"] = raw
			case strings.Contains(text, "The PDF specified is password protected"):
				content("pdf_password_protected", mediaPDFPassword(input.NonInteractive))
				selected["error"] = quote("invalid_request")
			case strings.Contains(text, "The PDF specified was not valid"):
				content("pdf_invalid", mediaPDFInvalid(input.NonInteractive))
				selected["error"] = quote("invalid_request")
			case api && status != nil && *status == 400 && strings.Contains(text, "image exceeds") && strings.Contains(text, "maximum"):
				content("image_too_large", mediaImageTooLarge(input.NonInteractive))
				selected["errorDetails"] = raw
			case api && status != nil && *status == 400 && strings.Contains(text, "image dimensions exceed") && strings.Contains(text, "many-image"):
				hint := "An image in the conversation exceeds the dimension limit for many-image requests (2000px). "
				if input.NonInteractive {
					hint += "Start a new session with fewer images."
				} else {
					hint += "Run /compact to remove old images from context, or start a new session."
				}
				content("image_too_large", hint)
				selected["error"] = quote("invalid_request")
				selected["errorDetails"] = raw
			case api && status != nil && *status == 413:
				content("request_too_large", mediaRequestTooLarge(input.MessagesForAPI, input.NonInteractive))
				selected["error"] = quote("invalid_request")
				if len(units) > 500 {
					units = units[:500]
				}
				selected["errorDetails"], err = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("request_too_large: "), protocol.QuoteJavaScriptUTF16(units)}, "")
				if err != nil {
					return nil, false, err
				}
			case api && status != nil && (*status == 400 || *status == 422) && input.MessagesForAPI != nil && mediaContainsImage(input.MessagesForAPI):
				if mediaEarlierNonMedia(text) {
					return nil, false, nil
				}
				content("image_unsupported", mediaImageUnsupported(input.NonInteractive))
				selected["error"] = quote("invalid_request")
				selected["errorDetails"] = raw
				selected["sourceModel"] = quote(input.Model)
			default:
				return nil, false, nil
			}
		}
	}
	record, err := factory.CreateOrderedAssistantAPIError(selected)
	if err != nil {
		return nil, true, err
	}
	envelope, err := mediaEnvelope(record)
	return envelope, true, err
}
func mediaOriginal(failure error) (json.RawMessage, bool, *int, bool) {
	switch value := failure.(type) {
	case *OriginalError:
		if value != nil {
			return value.message, false, nil, true
		}
	case *provider.SDKStreamError:
		if value == nil {
			return nil, false, nil, false
		}
		switch value.Class {
		case "APIError", "BadRequestError", "AuthenticationError", "PermissionDeniedError", "NotFoundError", "ConflictError", "UnprocessableEntityError", "RateLimitError", "InternalServerError":
		default:
			return nil, false, nil, false
		}
		if value.Kind != "http_api" && value.Kind != "sse_api" {
			return nil, false, nil, false
		}
		raw := value.MessageJSON
		if raw == nil {
			raw = quote(value.Message)
		}
		return raw, true, value.Status, true
	}
	return nil, false, nil, false
}
func unsupportedImageText(message string) bool {
	raw := strings.ToLower(message)
	if !strings.Contains(raw, "image") {
		return false
	}
	if strings.Contains(raw, "image_url") {
		for _, part := range []string{"not allowed", "not permitted", "disallowed", "forbidden"} {
			if strings.Contains(raw, part) {
				return true
			}
		}
		if strings.Contains(raw, "text") {
			for _, part := range []string{"expected", "input should be", "not one of", "permitted", "received", "unknown variant", "invalid value", "invalid type", "valid enumeration", "only text"} {
				if strings.Contains(raw, part) {
					return true
				}
			}
		}
	}
	for _, part := range []string{"not support", "not supported", "unsupported", "vision", "multimodal", "multi-modal", "modality"} {
		if strings.Contains(raw, part) {
			return true
		}
	}
	return false
}
func mediaEarlierNonMedia(text string) bool {
	lower := strings.ToLower(text)
	for _, part := range []string{"`tool_use` ids were found without `tool_result` blocks immediately after", "`tool_use` ids must be unique", "Your credit balance is too low"} {
		if strings.Contains(text, part) {
			return true
		}
	}
	// Invalid-model and organization/auth branches require the actual native
	// account state. Their guards are preserved instead of inventing that state.
	return strings.Contains(lower, "invalid model name") || strings.Contains(lower, "organization has been disabled") || strings.Contains(text, "anthropic-beta")
}
func mediaContainsImage(values []protocol.JSONObject) bool {
	for _, value := range values {
		raw, _ := value.MarshalJSON()
		if mediaRawContainsImage(raw) {
			return true
		}
	}
	return false
}
func mediaRawContainsImage(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	if raw[0] == '[' {
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) == nil {
			for _, value := range values {
				if mediaRawContainsImage(value) {
					return true
				}
			}
		}
		return false
	}
	var value protocol.JSONObject
	if value.UnmarshalJSON(raw) != nil || !value.IsObject() {
		return false
	}
	if value.String("type") == "image" {
		return true
	}
	for _, field := range value.OwnProperties() {
		if mediaRawContainsImage(field.Value) {
			return true
		}
	}
	return false
}
func mediaEnvelope(record protocol.JSONObject) (*streamhistory.Envelope, error) {
	var message protocol.JSONObject
	if err := message.UnmarshalJSON(record.Get("message")); err != nil {
		return nil, err
	}
	var blocks []protocol.JSONObject
	if err := json.Unmarshal(message.Get("content"), &blocks); err != nil {
		return nil, err
	}
	content := make([]*streamhistory.Block, len(blocks))
	for i, block := range blocks {
		content[i] = &streamhistory.Block{Object: block}
	}
	return &streamhistory.Envelope{Record: record, Message: message, Content: content, Usage: &streamhistory.Usage{Raw: message.Get("usage")}, UUID: record.String("uuid"), APIError: true}, nil
}
func mediaImageTooLarge(nonInteractive bool) string {
	if nonInteractive {
		return "Image was too large. Try resizing the image or using a different approach."
	}
	return "Image was too large. Double press esc to go back and try again with a smaller image."
}
func mediaImageUnsupported(nonInteractive bool) string {
	if nonInteractive {
		return "This model does not support images. Continue with text, or switch to a vision-capable model and send the image again."
	}
	return "This model does not support images. Double press esc to go back, switch to a vision-capable model, or continue with text."
}
func mediaPDFTooLarge(nonInteractive bool) string {
	if nonInteractive {
		return "PDF too large (max 100 pages, 20MB). Try reading the file a different way (e.g., extract text with pdftotext)."
	}
	return "PDF too large (max 100 pages, 20MB). Double press esc to go back and try again, or use pdftotext to convert to text first."
}
func mediaPDFPassword(nonInteractive bool) string {
	if nonInteractive {
		return "PDF is password protected. Try using a CLI tool to extract or convert the PDF."
	}
	return "PDF is password protected. Please double press esc to edit your message and try again."
}
func mediaPDFInvalid(nonInteractive bool) string {
	if nonInteractive {
		return "The PDF file was not valid. Try converting it to text first (e.g., pdftotext)."
	}
	return "The PDF file was not valid. Double press esc to go back and try again with a different file."
}

type mediaPayloadSize struct{ Total, Media int }

func mediaRequestTooLarge(history []protocol.JSONObject, nonInteractive bool) string {
	rejected := "Request too large: your provider or relay rejected it (HTTP 413)."
	next := "compact the conversation or start a new session"
	if !nonInteractive {
		next = "run /compact or double press esc to go back past the large content"
	}
	sentence := strings.ToUpper(next[:1]) + next[1:]
	if history == nil {
		return rejected + " The size limit is set by that endpoint. " + sentence + "."
	}
	size := mediaPayloadSize{}
	for _, value := range history {
		message, _ := value.Object("message")
		part := mediaMeasureContent(message.Get("content"))
		size.Total += part.Total
		size.Media += part.Media
	}
	if size.Media == 0 {
		return fmt.Sprintf("%s This conversation is about %s and none of it is images or documents. %s; a relay can enforce a lower request size limit than its upstream provider.", rejected, mediaFormatBytes(size.Total), sentence)
	}
	return fmt.Sprintf("%s This conversation is about %s, of which %s is images or documents. Earlier images and documents are replaced with placeholders in the next request; if it still fails, %s.", rejected, mediaFormatBytes(size.Total), mediaFormatBytes(size.Media), next)
}
func mediaMeasureContent(raw json.RawMessage) mediaPayloadSize {
	if units, err := protocol.DecodeJavaScriptJSONString(raw); err == nil {
		return mediaPayloadSize{Total: mediaSourceByteLength(units)}
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || parts == nil {
		return mediaPayloadSize{}
	}
	size := mediaPayloadSize{}
	for _, part := range parts {
		value := mediaMeasureBlock(part)
		size.Total += value.Total
		size.Media += value.Media
	}
	return size
}
func mediaMeasureBlock(raw json.RawMessage) mediaPayloadSize {
	if _, err := protocol.DecodeJavaScriptJSONString(raw); err == nil {
		return mediaMeasureContent(raw)
	}
	var block protocol.JSONObject
	if block.UnmarshalJSON(raw) != nil || !block.IsObject() {
		return mediaPayloadSize{}
	}
	switch block.String("type") {
	case "image", "document":
		source, _ := block.Object("source")
		for _, key := range []string{"data", "url"} {
			if units, err := protocol.DecodeJavaScriptJSONString(source.Get(key)); err == nil {
				return mediaPayloadSize{len(units), len(units)}
			}
		}
		return mediaPayloadSize{}
	case "text":
		if _, err := protocol.DecodeJavaScriptJSONString(block.Get("text")); err == nil {
			return mediaMeasureContent(block.Get("text"))
		}
	case "tool_result":
		return mediaMeasureContent(block.Get("content"))
	}
	value, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		return mediaPayloadSize{}
	}
	return mediaPayloadSize{Total: len(value)}
}
func mediaFormatBytes(bytes int) string {
	if bytes < 1024 {
		return strconv.Itoa(bytes) + " bytes"
	}
	value := float64(bytes) / 1024
	unit := "KB"
	if value >= 1024 {
		value /= 1024
		unit = "MB"
	}
	if value >= 1024 {
		value /= 1024
		unit = "GB"
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", value), ".0") + unit
}

// The pinned Bun Source's Buffer.byteLength counts a lone UTF-16 surrogate as
// two bytes, even though Buffer.from writes the three-byte replacement. Only
// this diagnostic size estimate uses that Source primitive; actual HTTP bytes
// keep the existing correct UTF-8 encoder.
func mediaSourceByteLength(units []uint16) int {
	total := 0
	for i := 0; i < len(units); i++ {
		u := units[i]
		switch {
		case u < 128:
			total++
		case u < 2048:
			total += 2
		case u >= 0xd800 && u <= 0xdbff && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff:
			total += 4
			i++
		case u >= 0xd800 && u <= 0xdfff:
			total += 2
		default:
			total += 3
		}
	}
	return total
}
