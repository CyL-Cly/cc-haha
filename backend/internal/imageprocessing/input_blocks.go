package imageprocessing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// InputBlocks performs Source processUserInput's image pass. Document and URL
// blocks are forwarded unchanged; an SDK base64 image is decoded and rebuilt
// from the actual codec output, including the original field-dropping behavior.
func InputBlocks(ctx context.Context, codec Codec, content json.RawMessage) (json.RawMessage, []json.RawMessage, error) {
	if _, err := protocol.DecodeJavaScriptJSONString(content); err == nil {
		return append(json.RawMessage(nil), content...), []json.RawMessage{}, nil
	}
	var blocks []protocol.JSONObject
	if err := json.Unmarshal(content, &blocks); err != nil || blocks == nil {
		return nil, nil, fmt.Errorf("input content must be a string or content block array")
	}
	texts := []json.RawMessage{}
	for i, block := range blocks {
		if err := context.Cause(ctx); err != nil {
			return nil, nil, err
		}
		if block.String("type") != "image" {
			continue
		}
		source, ok := block.Object("source")
		if !ok {
			return nil, nil, fmt.Errorf("image source must be an object")
		}
		if source.String("type") != "base64" {
			continue
		}
		units, err := protocol.DecodeJavaScriptJSONString(source.Get("data"))
		if err != nil {
			return nil, nil, err
		}
		data := DecodeInputBase64(units)
		ext := "png"
		if media := source.String("media_type"); media != "" {
			parts := strings.Split(media, "/")
			if len(parts) > 1 && parts[1] != "" {
				ext = parts[1]
			}
		}
		output, err := Normalize(codec, data, len(data), ext)
		if err != nil {
			return nil, nil, err
		}
		rebuilt := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: inputQuote("base64")}, protocol.JSONField{Name: "media_type", Value: inputQuote("image/" + output.MediaType)}, protocol.JSONField{Name: "data", Value: inputQuote(base64.StdEncoding.EncodeToString(output.Buffer))})
		encoded, _ := json.Marshal(rebuilt)
		blocks[i] = protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: inputQuote("image")}, protocol.JSONField{Name: "source", Value: encoded})
		if text := MetadataText(output.Dimensions, ""); text != "" {
			texts = append(texts, inputQuote(text))
		}
	}
	if err := context.Cause(ctx); err != nil {
		return nil, nil, err
	}
	encoded, err := json.Marshal(blocks)
	return encoded, texts, err
}
func inputQuote(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }

// Buffer.from(data, 'base64') accepts both alphabets, omitted padding and
// nonalphabet characters, and stops at the first padding marker.
func DecodeInputBase64(units []uint16) []byte {
	data := make([]byte, 0, len(units))
	for _, u := range units {
		if u == '=' {
			break
		}
		switch {
		case u >= 'a' && u <= 'z', u >= 'A' && u <= 'Z', u >= '0' && u <= '9', u == '+', u == '/':
			data = append(data, byte(u))
		case u == '-':
			data = append(data, '+')
		case u == '_':
			data = append(data, '/')
		}
	}
	if len(data)%4 == 1 {
		data = data[:len(data)-1]
	}
	decoded, _ := base64.RawStdEncoding.DecodeString(string(data))
	return decoded
}
