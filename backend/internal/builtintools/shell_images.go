package builtintools

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var shellImageOutput = regexp.MustCompile(`(?i)^data:image/[a-z0-9.+_-]+;base64,`)
var shellDataURI = regexp.MustCompile(`^data:([^;]+);base64,([^\n\r\x{2028}\x{2029}]+)$`)

func shellImageResult(data protocol.JSONObject, id json.RawMessage) (protocol.JSONObject, bool) {
	parsed := shellDataURI.FindStringSubmatch(protocol.TrimJavaScriptString(data.String("stdout")))
	if len(parsed) != 3 {
		return protocol.JSONObject{}, false
	}
	block := protocol.NewJSONObject(field("type", "image"), field("source", protocol.NewJSONObject(field("type", "base64"), field("media_type", parsed[1]), field("data", parsed[2]))))
	return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), field("content", []protocol.JSONObject{block})), true
}

func (s *Service) normalizeShellImage(ctx context.Context, scope *cooperative.Scope, data protocol.JSONObject) (protocol.JSONObject, error) {
	if !shellImageOutput.MatchString(data.String("stdout")) {
		return data, nil
	}
	source := data.String("stdout")
	if path := data.String("persistedOutputPath"); path != "" {
		future, err := native(s, scope, func() (string, error) {
			f, err := os.Open(path)
			if err != nil {
				return "", err
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil {
				return "", err
			}
			if info.Size() > 20*1024*1024 {
				return "", nil
			}
			bytes, err := io.ReadAll(io.LimitReader(f, 20*1024*1024+1))
			if len(bytes) > 20*1024*1024 {
				return "", nil
			}
			return string(protocol.DecodeJavaScriptUTF8(bytes)), err
		})
		if err != nil {
			return data, err
		}
		source, err = future.Await(scope)
		if err != nil {
			return data, err
		}
	}
	parsed := shellDataURI.FindStringSubmatch(protocol.TrimJavaScriptString(source))
	if len(parsed) != 3 {
		data.Set("isImage", raw(false))
		return data, nil
	}
	if s.options.Images == nil || s.options.Images.NormalizeShellData == nil {
		return data, capability("actual shell image native normalization")
	}
	parts := strings.Split(parsed[1], "/")
	extension := "png"
	if len(parts) > 1 && parts[1] != "" {
		extension = parts[1]
	}
	bytes := imageprocessing.DecodeInputBase64(utf16.Encode([]rune(parsed[2])))
	future, err := s.options.Images.NormalizeShellData(ctx, scope, bytes, extension)
	if err != nil {
		return data, err
	}
	output, err := future.Await(scope)
	if err != nil {
		return data, err
	}
	data.Set("stdout", quote(output))
	data.Set("isImage", raw(true))
	return data, nil
}
