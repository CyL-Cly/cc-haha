package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopattachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func terminalInputText(input tui.Input) (json.RawMessage, error) {
	if input.TextJSON == nil {
		return raw(input.Text), nil
	}
	units, err := protocol.DecodeJavaScriptJSONString(input.TextJSON)
	if err != nil {
		return nil, fmt.Errorf("terminal input text: %w", err)
	}
	return protocol.QuoteJavaScriptUTF16(units), nil
}

func (s *Session) prepareTerminalMedia(ctx context.Context, input tui.Input) (context.Context, json.RawMessage, error) {
	if len(input.Attachments) == 0 && len(input.PastedContents) == 0 {
		return ctx, nil, nil
	}
	codec, _ := imageprocessing.BundledCodec(s.native.ImageLibraryPath)
	return terminalInputMedia(ctx, input, s.native.Home, s.native.ConfigHomeDirectory(), s.process.Controller.SessionID(), codec)
}

func terminalInputMedia(ctx context.Context, input tui.Input, home, config, sessionID string, codec imageprocessing.Codec) (context.Context, json.RawMessage, error) {
	text, err := terminalInputText(input)
	if err != nil {
		return ctx, nil, err
	}
	if input.TextJSON != nil {
		if err := json.Unmarshal(text, &input.Text); err != nil {
			return ctx, nil, err
		}
	}
	if len(input.Attachments) == 0 && len(input.PastedContents) == 0 {
		return ctx, nil, nil
	}
	if input.Mode != "" && input.Mode != "prompt" {
		return ctx, nil, fmt.Errorf("Mode: %s requires a string input.", input.Mode)
	}
	blocks := []protocol.JSONObject{}
	if len(input.Attachments) > 0 {
		refs, err := json.Marshal(input.Attachments)
		if err != nil {
			return ctx, nil, err
		}
		encoded, err := desktopattachments.BuildJSON(text, sessionID, refs, desktopattachments.Options{HomeDir: home, ConfigDir: config, NewID: newID, ImageProcessor: func(data []byte, size int, extension string) (imageprocessing.Result, error) {
			return imageprocessing.Normalize(codec, data, size, extension)
		}})
		if err != nil {
			return ctx, nil, err
		}
		if err = json.Unmarshal(encoded, &blocks); err != nil {
			return ctx, nil, err
		}
	} else if strings.TrimSpace(input.Text) != "" {
		blocks = append(blocks, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("text")}, protocol.JSONField{Name: "text", Value: text}))
	}
	keys := make([]int, 0, len(input.PastedContents))
	for id := range input.PastedContents {
		keys = append(keys, id)
	}
	sort.Ints(keys)
	ids := []int{}
	metadata := []json.RawMessage{}
	if config == "" {
		config = filepath.Join(home, ".claude")
	}
	for _, id := range keys {
		if err := context.Cause(ctx); err != nil {
			return ctx, nil, err
		}
		paste := input.PastedContents[id]
		if paste.Type != "image" || paste.Content == "" {
			continue // Source isValidImagePaste; text is already expanded once.
		}
		media := paste.MediaType
		if media == "" {
			media = "image/png"
		}
		ext := "png"
		if parts := strings.Split(media, "/"); len(parts) > 1 && parts[1] != "" {
			ext = parts[1]
		}
		data := imageprocessing.DecodeInputBase64(utf16.Encode([]rune(paste.Content)))
		processed, err := imageprocessing.Normalize(codec, data, len(data), ext)
		if err != nil {
			return ctx, nil, err
		}
		// Source stores the original clipboard bytes with mode 0600 before
		// resizing the API block; failure to cache does not discard the image.
		path := ""
		directory := filepath.Join(config, "image-cache", sessionID)
		if err := os.MkdirAll(directory, 0755); err == nil {
			candidate := filepath.Join(directory, fmt.Sprintf("%d.%s", paste.ID, ext))
			if err := os.WriteFile(candidate, data, 0600); err == nil {
				path = candidate
			}
		}
		if paste.SourcePath != "" {
			path = paste.SourcePath
		}
		dimensions := processed.Dimensions
		if dimensions == nil && len(paste.Dimensions) > 0 {
			var original imageprocessing.Dimensions
			if json.Unmarshal(paste.Dimensions, &original) == nil {
				dimensions = &original
			}
		}
		if text := imageprocessing.MetadataText(dimensions, path); text != "" {
			metadata = append(metadata, raw(text))
		}
		ids = append(ids, paste.ID)
		source := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("base64")}, protocol.JSONField{Name: "media_type", Value: raw("image/" + processed.MediaType)}, protocol.JSONField{Name: "data", Value: raw(base64.StdEncoding.EncodeToString(processed.Buffer))})
		blocks = append(blocks, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("image")}, protocol.JSONField{Name: "source", Value: raw(source)}))
	}
	if len(ids) == 0 && len(input.Attachments) == 0 {
		return ctx, nil, nil
	}
	// The SDK parser reads the final text block for slash dispatch. Pasted
	// images retain their Source text-first order for ordinary prompts.
	if strings.HasPrefix(input.Text, "/") && len(blocks) > 1 && blocks[0].String("type") == "text" {
		first := blocks[0]
		blocks = append(blocks[1:], first)
	}
	encoded, err := json.Marshal(blocks)
	if err != nil {
		return ctx, nil, err
	}
	return sdkhost.WithInputImageMetadata(ctx, ids, metadata), encoded, nil
}
