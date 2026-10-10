package imruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
)

type FeishuDownloadParams struct {
	MessageID, FileKey Text
	Kind               string
	FileName           Text
	SessionID          string
}
type FeishuMedia struct {
	Client *FeishuClient
	Store  *AttachmentStore
}

func NewFeishuMedia(client *FeishuClient, store *AttachmentStore) *FeishuMedia {
	return &FeishuMedia{client, store}
}
func (m *FeishuMedia) DownloadResource(ctx context.Context, p FeishuDownloadParams) (LocalAttachment, error) {
	name := p.FileName
	if len(name) == 0 {
		name = p.FileKey.Clone()
		if p.Kind == "image" {
			name = name.Join(NewText(".png"))
		}
	}
	target, e := m.Store.ResolvePath("feishu", p.SessionID, name.String())
	if e != nil {
		return LocalAttachment{}, e
	}
	ctx, done, e := m.Client.begin(ctx)
	if e != nil {
		return LocalAttachment{}, e
	}
	defer done()
	data, e := m.Client.http(ctx, "GET", "/open-apis/im/v1/messages/"+p.MessageID.String()+"/resources/"+p.FileKey.String(), feishuObject(feishuField("type", p.Kind)), []byte(`{}`), "application/json", true)
	if e != nil {
		return LocalAttachment{}, e
	}
	// Installed node-sdk writeFile writes directly to the resolved path; the
	// AttachmentStore .part writer is only the Source Buffer fallback branch.
	if e = os.WriteFile(target, data, 0666); e != nil {
		return LocalAttachment{}, e
	}
	return LocalAttachment{Kind: p.Kind, Name: name.String(), Path: target, Buffer: data, Size: int64(len(data)), MIME: FeishuGuessMIME(name.String(), p.Kind)}, nil
}
func FeishuFileType(name string) string {
	switch strings.ToLower(feishuExtname(name)) {
	case ".opus":
		return "opus"
	case ".mp4":
		return "mp4"
	case ".pdf":
		return "pdf"
	case ".doc", ".docx":
		return "doc"
	case ".xls", ".xlsx":
		return "xls"
	case ".ppt", ".pptx":
		return "ppt"
	}
	return "stream"
}
func FeishuGuessMIME(name, kind string) string {
	ext := strings.ToLower(strings.TrimPrefix(feishuExtname(name), "."))
	if kind == "image" {
		if v := map[string]string{"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp", "heic": "image/heic"}[ext]; v != "" {
			return v
		}
		return "image/png"
	}
	if v := map[string]string{"pdf": "application/pdf", "doc": "application/msword", "docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "xls": "application/vnd.ms-excel", "xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "ppt": "application/vnd.ms-powerpoint", "pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation", "txt": "text/plain", "json": "application/json"}[ext]; v != "" {
		return v
	}
	return "application/octet-stream"
}
func (m *FeishuMedia) upload(ctx context.Context, path string, fields protocol.JSONObject, key, filename string, data []byte) (json.RawMessage, error) {
	ctx, done, e := m.Client.begin(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, f := range fields.OwnProperties() {
		if e = writer.WriteField(jsString(f.NameJSON), jsString(f.Value)); e != nil {
			return nil, e
		}
	}
	// Actual axios form-data Buffer uploads have no filename parameter and
	// carry application/octet-stream; preserve those original multipart parts.
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"`, key))
	header.Set("Content-Type", "application/octet-stream")
	part, e := writer.CreatePart(header)
	if e != nil {
		return nil, e
	}
	if _, e = part.Write(data); e != nil {
		return nil, e
	}
	if e = writer.Close(); e != nil {
		return nil, e
	}
	response, e := m.Client.http(ctx, "POST", path, protocol.NewJSONObject(), body.Bytes(), writer.FormDataContentType(), true)
	if e != nil {
		return nil, e
	}
	// node-sdk image/file.create unwrap res.data; preserve the original media
	// helper's second data lookup, including its missing-key error branch.
	return object(object(object(feishuResponse(response)).Get("data")).Get("data")).Get(key + "_key"), nil
}
func (m *FeishuMedia) UploadImage(ctx context.Context, data []byte, mime string) (json.RawMessage, error) {
	v, e := m.upload(ctx, "/open-apis/im/v1/images", feishuObject(feishuField("image_type", "message")), "image", "blob", data)
	if e != nil {
		return nil, e
	}
	if !credentialTruthy(v) {
		return nil, fmt.Errorf("[FeishuMedia] uploadImage: missing image_key")
	}
	return v, nil
}
func (m *FeishuMedia) UploadFile(ctx context.Context, data []byte, name string) (json.RawMessage, error) {
	v, e := m.upload(ctx, "/open-apis/im/v1/files", feishuObject(feishuField("file_type", FeishuFileType(name)), feishuField("file_name", name)), "file", "blob", data)
	if e != nil {
		return nil, e
	}
	if !credentialTruthy(v) {
		return nil, fmt.Errorf("[FeishuMedia] uploadFile: missing file_key")
	}
	return v, nil
}
func (m *FeishuMedia) SendImageMessage(ctx context.Context, chat Text, key json.RawMessage) error {
	_, e := m.Client.CreateMessage(ctx, chat, "image", NewText(string(raw(feishuObject(protocol.JSONField{Name: "image_key", Value: key})))), nil)
	return e
}
func (m *FeishuMedia) SendFileMessage(ctx context.Context, chat Text, key json.RawMessage) error {
	_, e := m.Client.CreateMessage(ctx, chat, "file", NewText(string(raw(feishuObject(protocol.JSONField{Name: "file_key", Value: key})))), nil)
	return e
}

func feishuExtname(name string) string {
	base := filepath.Base(name)
	if base == "." || base == ".." {
		return ""
	}
	at := strings.LastIndex(base, ".")
	if at <= 0 {
		return ""
	}
	return base[at:]
}
