package tui

import (
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"math"
)

func presentationNumber(value json.RawMessage) (float64, bool) {
	var number float64
	if len(value) == 0 || string(value) == "null" || json.Unmarshal(value, &number) != nil || math.IsInf(number, 0) || math.IsNaN(number) {
		return 0, false
	}
	return number, true
}
func presentationStrings(object protocol.JSONObject, keys ...string) bool {
	for _, key := range keys {
		if _, ok := presentationString(object.Get(key)); !ok {
			return false
		}
	}
	return true
}
func presentationNumbers(object protocol.JSONObject, keys ...string) bool {
	for _, key := range keys {
		if _, ok := presentationNumber(object.Get(key)); !ok {
			return false
		}
	}
	return true
}
func presentationFileSize(size float64) string { return toolschema.FormatFileSize(size) }
func presentationNumberText(number float64) string {
	encoded, _ := json.Marshal(number)
	text, _ := protocol.ParseAndStringifyJavaScriptJSON(encoded)
	return string(text)
}

// Resumed records bypass runtime tool output validation. Mirror Read's original
// outputSchema before its renderer, so an old/corrupt DTO never displays fake
// counts or exposes its model-only mapped reminder as a fallback string.
func readResultPresentation(result protocol.JSONObject) string {
	if !result.IsObject() {
		return ""
	}
	file, ok := result.Object("file")
	if !ok {
		return ""
	}
	switch result.String("type") {
	case "text":
		if !presentationStrings(file, "filePath", "content") || !presentationNumbers(file, "numLines", "startLine", "totalLines") {
			return ""
		}
		count, _ := presentationNumber(file.Get("numLines"))
		label := "lines"
		if count == 1 {
			label = "line"
		}
		return "Read " + presentationNumberText(count) + " " + label
	case "file_unchanged":
		if !presentationStrings(file, "filePath") {
			return ""
		}
		return "Unchanged since last read"
	case "notebook":
		if !presentationStrings(file, "filePath") {
			return ""
		}
		var cells []json.RawMessage
		if json.Unmarshal(file.Get("cells"), &cells) != nil || cells == nil {
			return ""
		}
		if len(cells) == 0 {
			return "No cells found in notebook"
		}
		return fmt.Sprintf("Read %d cells", len(cells))
	case "image":
		if !presentationStrings(file, "base64", "type") || !presentationNumbers(file, "originalSize") {
			return ""
		}
		switch file.String("type") {
		case "image/jpeg", "image/png", "image/gif", "image/webp":
		default:
			return ""
		}
		if dimensions := file.Get("dimensions"); dimensions != nil {
			value := presentationObject(dimensions)
			if !value.IsObject() {
				return ""
			}
			for _, key := range []string{"originalWidth", "originalHeight", "displayWidth", "displayHeight"} {
				if value.Get(key) != nil && !presentationNumbers(value, key) {
					return ""
				}
			}
		}
		size, _ := presentationNumber(file.Get("originalSize"))
		return "Read image (" + presentationFileSize(size) + ")"
	case "pdf":
		if !presentationStrings(file, "filePath", "base64") || !presentationNumbers(file, "originalSize") {
			return ""
		}
		size, _ := presentationNumber(file.Get("originalSize"))
		return "Read PDF (" + presentationFileSize(size) + ")"
	case "parts":
		if !presentationStrings(file, "filePath", "outputDir") || !presentationNumbers(file, "originalSize", "count") {
			return ""
		}
		size, _ := presentationNumber(file.Get("originalSize"))
		count, _ := presentationNumber(file.Get("count"))
		label := "pages"
		if count == 1 {
			label = "page"
		}
		return "Read " + presentationNumberText(count) + " " + label + " (" + presentationFileSize(size) + ")"
	}
	return ""
}
