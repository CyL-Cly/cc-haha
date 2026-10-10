package imagegeneration

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func MultipartBody(fields protocol.JSONObject, images []PreparedImage, smart bool, boundary string) ([]byte, string, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.SetBoundary(boundary); err != nil {
		return nil, "", err
	}
	for _, property := range fields.OwnProperties() {
		if property.Value == nil {
			continue
		}
		nameUnits, err := protocol.DecodeJavaScriptJSONString(property.NameJSON)
		if err != nil {
			return nil, "", err
		}
		raw, err := protocol.ExactJavaScriptStringJSON(property.Value)
		if err != nil {
			return nil, "", err
		}
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		if err != nil {
			return nil, "", err
		}
		if err = writer.WriteField(string(utf16.Decode(nameUnits)), string(utf16.Decode(units))); err != nil {
			return nil, "", err
		}
	}
	name := "image[]"
	if smart {
		name = "image"
	}
	escape := func(s string) string { return strings.NewReplacer("\r", "%0D", "\n", "%0A", "\"", "%22").Replace(s) }
	for _, image := range images {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, name, escape(image.FileName)))
		header.Set("Content-Type", image.MIMEType)
		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, "", err
		}
		if _, err = part.Write(image.Bytes); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
}
