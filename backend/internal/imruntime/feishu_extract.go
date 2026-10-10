package imruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type FeishuPendingDownload struct {
	Kind     string `json:"kind"`
	FileKey  Text   `json:"fileKey"`
	FileName *Text  `json:"fileName,omitempty"`
}
type FeishuInboundPayload struct {
	Text             Text                    `json:"text"`
	PendingDownloads []FeishuPendingDownload `json:"pendingDownloads"`
}

func FeishuExtractInbound(content Text, msgType string) (FeishuInboundPayload, error) {
	result := FeishuInboundPayload{Text: Text{}, PendingDownloads: []FeishuPendingDownload{}}
	p := []byte(protocol.JavaScriptJSONSourceUTF16(content))
	if !json.Valid(p) {
		return result, nil
	}
	v := object(p)
	if string(bytes.TrimSpace(p)) == "null" && (msgType == "text" || msgType == "image" || msgType == "file" || msgType == "file_archive" || msgType == "post") {
		return result, fmt.Errorf("null is not an object")
	}
	switch msgType {
	case "text":
		if value := v.Get("text"); isString(value) {
			result.Text = TextJSON(value)
		}
	case "image":
		if value := v.Get("image_key"); isString(value) && credentialTruthy(value) {
			result.PendingDownloads = append(result.PendingDownloads, FeishuPendingDownload{Kind: "image", FileKey: TextJSON(value)})
		}
	case "file", "file_archive":
		if value := v.Get("file_key"); isString(value) && credentialTruthy(value) {
			file := FeishuPendingDownload{Kind: "file", FileKey: TextJSON(value)}
			if name := v.Get("file_name"); isString(name) {
				text := TextJSON(name)
				file.FileName = &text
			}
			result.PendingDownloads = append(result.PendingDownloads, file)
		}
	case "post":
		nodes := object(v.Get("zh_cn")).Get("content")
		if len(nodes) == 0 || string(nodes) == "null" {
			nodes = object(v.Get("en_us")).Get("content")
		}
		if len(nodes) == 0 || string(nodes) == "null" {
			nodes = []byte(`[]`)
		}
		var rows []json.RawMessage
		if json.Unmarshal(nodes, &rows) != nil {
			return result, fmt.Errorf("nodes.flat is not a function")
		}
		flat := []json.RawMessage{}
		for _, row := range rows {
			if len(row) > 0 && row[0] == '[' {
				var inner []json.RawMessage
				_ = json.Unmarshal(row, &inner)
				flat = append(flat, inner...)
			} else {
				flat = append(flat, row)
			}
		}
		for _, node := range flat {
			item := object(node)
			switch item.String("tag") {
			case "text", "md":
				text := item.Get("text")
				if len(text) == 0 || string(text) == "null" {
					text = item.Get("content")
				}
				if isString(text) {
					result.Text = result.Text.Join(TextJSON(text))
				}
			case "img":
				if key := item.Get("image_key"); isString(key) {
					result.PendingDownloads = append(result.PendingDownloads, FeishuPendingDownload{Kind: "image", FileKey: TextJSON(key)})
				}
			case "file":
				if key := item.Get("file_key"); isString(key) {
					file := FeishuPendingDownload{Kind: "file", FileKey: TextJSON(key)}
					if name := item.Get("file_name"); isString(name) {
						text := TextJSON(name)
						file.FileName = &text
					}
					result.PendingDownloads = append(result.PendingDownloads, file)
				}
			}
		}
	}
	return result, nil
}
