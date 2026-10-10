package requestcontext

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// APIImageMaxBase64Size is Source's limit on encoded UTF-16 string length,
// rather than decoded image bytes. Resizing belongs to the image producer.
const APIImageMaxBase64Size = 5 * 1024 * 1024

// ValidateProcessedImageBlock admits the actual base64 image output of the
// native/UI image producer. SDK URL images pass through unchanged; image
// normalization never downloads them or grants filesystem authorization.
func ValidateProcessedImageBlock(block protocol.JSONObject) error {
	if block.String("type") != "image" {
		return unsupported("processed image block")
	}
	source, err := parseObject(block.Get("source"))
	if err != nil || !source.IsObject() {
		return unsupported("processed base64 image source")
	}
	if source.String("type") == "url" {
		if _, err := protocol.DecodeJavaScriptJSONString(source.Get("url")); err != nil {
			return unsupported("image URL source")
		}
		return nil
	}
	if source.String("type") != "base64" {
		return unsupported("processed base64 image source")
	}
	switch source.String("media_type") {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return unsupported("processed image media type")
	}
	if _, err := protocol.DecodeJavaScriptJSONString(source.Get("data")); err != nil {
		return unsupported("processed image data")
	}
	return nil
}

type OversizedImage struct{ Index, Size int }

// ImageSizeError preserves the Source API-boundary error text and image index.
type ImageSizeError struct{ Images []OversizedImage }

func (failure *ImageSizeError) Error() string {
	limit := imageFileSize(APIImageMaxBase64Size)
	if len(failure.Images) == 1 {
		return fmt.Sprintf("Image base64 size (%s) exceeds API limit (%s). Please resize the image before sending.", imageFileSize(failure.Images[0].Size), limit)
	}
	parts := make([]string, 0, len(failure.Images))
	for _, image := range failure.Images {
		parts = append(parts, fmt.Sprintf("Image %d: %s", image.Index, imageFileSize(image.Size)))
	}
	return fmt.Sprintf("%d images exceed the API limit (%s): %s. Please resize these images before sending.", len(parts), limit, strings.Join(parts, ", "))
}

func imageFileSize(size int) string {
	if size < 1024 {
		return fmt.Sprintf("%d bytes", size)
	}
	value, unit := float64(size)/1024, "KB"
	if value >= 1024 {
		value, unit = value/1024, "MB"
	}
	if value >= 1024 {
		value, unit = value/1024, "GB"
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", value), ".0") + unit
}

// ValidateUserImagesForAPI matches validateImagesForAPI: only top-level
// base64 images in wrapped user messages are counted, in encounter order.
func ValidateUserImagesForAPI(history []protocol.JSONObject) error {
	oversized := []OversizedImage{}
	index := 0
	for _, envelope := range history {
		if envelope.String("type") != "user" {
			continue
		}
		for _, block := range blocks(content(envelope)) {
			if block.String("type") != "image" {
				continue
			}
			source, err := parseObject(block.Get("source"))
			if err != nil || source.String("type") != "base64" {
				continue
			}
			data, err := protocol.DecodeJavaScriptJSONString(source.Get("data"))
			if err != nil {
				continue
			}
			index++
			if len(data) > APIImageMaxBase64Size {
				oversized = append(oversized, OversizedImage{Index: index, Size: len(data)})
			}
		}
	}
	if len(oversized) != 0 {
		return &ImageSizeError{Images: oversized}
	}
	return nil
}

type historyMediaPlan struct {
	targets map[string]map[string]bool
	through int
}

func historyMediaUUID(envelope protocol.JSONObject) string {
	value, err := protocol.ParseAndStringifyJavaScriptJSON(envelope.Get("uuid"))
	if err != nil {
		return string(envelope.Get("uuid"))
	}
	return string(value)
}

func imageRecoveryPlan(history []protocol.JSONObject, model string, nonInteractive bool) (historyMediaPlan, error) {
	plan := historyMediaPlan{targets: map[string]map[string]bool{}, through: -1}
	legacy := map[string]string{}
	if nonInteractive {
		legacy["Image was too large. Try resizing the image or using a different approach."] = "image"
		legacy["This model does not support images. Continue with text, or switch to a vision-capable model and send the image again."] = "image"
		legacy["PDF is password protected. Try using a CLI tool to extract or convert the PDF."] = "document"
		legacy["The PDF file was not valid. Try converting it to text first (e.g., pdftotext)."] = "document"
		legacy["PDF too large (max 100 pages, 20MB). Try reading the file a different way (e.g., extract text with pdftotext)."] = "document"
	} else {
		legacy["Image was too large. Double press esc to go back and try again with a smaller image."] = "image"
		legacy["This model does not support images. Double press esc to go back, switch to a vision-capable model, or continue with text."] = "image"
		legacy["PDF is password protected. Please double press esc to edit your message and try again."] = "document"
		legacy["The PDF file was not valid. Double press esc to go back and try again with a different file."] = "document"
		legacy["PDF too large (max 100 pages, 20MB). Double press esc to go back and try again, or use pdftotext to convert to text first."] = "document"
	}
	for index, envelope := range history {
		if !IsSyntheticAPIErrorMessage(envelope) {
			continue
		}
		if _, err := protocol.DecodeJavaScriptJSONString(envelope.Get("sourceModel")); err == nil && model != "" && stringValue(envelope.Get("sourceModel")) != model {
			continue
		}
		if err := validateSyntheticAPIErrorAnchor(envelope); err != nil {
			return plan, err
		}
		text := ""
		parts := blocks(content(envelope))
		if len(parts) > 0 && parts[0].String("type") == "text" {
			text = parts[0].String("text")
		}
		codeRaw := envelope.Get("businessErrorCode")
		_, stringCode := protocol.DecodeJavaScriptJSONString(codeRaw)
		code := envelope.String("businessErrorCode")
		requestTooLarge := stringCode == nil && code == "request_too_large"
		if stringCode != nil {
			requestTooLarge = text == "Request too large (max 20MB). Try with a smaller file." || text == "Request too large (max 20MB). Double press esc to go back and try with a smaller file."
		}
		if requestTooLarge {
			plan.through = index
			continue
		}
		blockType := ""
		switch code {
		case "image_too_large", "image_unsupported":
			blockType = "image"
		case "pdf_too_large", "pdf_password_protected", "pdf_invalid":
			blockType = "document"
		}
		if blockType == "" {
			blockType = legacy[text]
		}
		if blockType == "" {
			continue
		}
		for preceding := index - 1; preceding >= 0; preceding-- {
			candidate := history[preceding]
			if candidate.String("type") == "user" {
				key := historyMediaUUID(candidate)
				if plan.targets[key] == nil {
					plan.targets[key] = map[string]bool{}
				}
				plan.targets[key][blockType] = true
				break
			}
			if !IsSyntheticAPIErrorMessage(candidate) {
				break
			}
		}
	}
	return plan, nil
}

func stripHistoryMedia(envelope protocol.JSONObject, types map[string]bool) (protocol.JSONObject, bool) {
	raw := content(envelope)
	if len(raw) == 0 || raw[0] != '[' {
		return envelope, false
	}
	parts := blocks(raw)
	kept := make([]protocol.JSONObject, 0, len(parts))
	for _, part := range parts {
		if !types[part.String("type")] {
			kept = append(kept, part)
		}
	}
	if len(kept) == 0 {
		return envelope, true
	}
	if len(kept) == len(parts) {
		return envelope, false
	}
	value, _ := json.Marshal(kept)
	return replaceContent(envelope, value), false
}

func replaceHistoryMedia(envelope protocol.JSONObject) protocol.JSONObject {
	raw := content(envelope)
	if len(raw) == 0 || raw[0] != '[' {
		return envelope
	}
	parts := blocks(raw)
	changed := false
	placeholder := func(block protocol.JSONObject) protocol.JSONObject {
		kind := block.String("type")
		if kind == "image" || kind == "document" {
			changed = true
			return object(field("type", quoted("text")), field("text", quoted("["+kind+"]")))
		}
		return block
	}
	for index, block := range parts {
		if block.String("type") == "tool_result" && len(block.Get("content")) > 0 && block.Get("content")[0] == '[' {
			nested := blocks(block.Get("content"))
			nestedChanged := false
			for i, part := range nested {
				if part.String("type") == "image" || part.String("type") == "document" {
					nestedChanged = true
					nested[i] = placeholder(part)
				}
			}
			if nestedChanged {
				value, _ := json.Marshal(nested)
				block = block.Clone()
				block.Set("content", value)
			}
		}
		parts[index] = placeholder(block)
	}
	if !changed {
		return envelope
	}
	value, _ := json.Marshal(parts)
	return replaceContent(envelope, value)
}
