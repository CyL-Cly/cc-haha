package imruntime

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storagerelocations"
)

const ImageMaxBytes = 10 * 1024 * 1024
const FileMaxBytes = 30 * 1024 * 1024

type AttachmentLimit struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

func CheckAttachmentLimit(kind string, size int64, mime string) AttachmentLimit {
	if kind == "image" {
		if size > ImageMaxBytes {
			return AttachmentLimit{false, "too_large", fmt.Sprintf("📎 图片过大(%.1f MB),请控制在 10 MB 以内", float64(size)/(1024*1024))}
		}
		if mime != "" && mime != "image/jpeg" && mime != "image/png" && mime != "image/gif" && mime != "image/webp" {
			return AttachmentLimit{false, "unsupported_mime", "📎 暂不支持此图片格式(" + mime + ")"}
		}
		return AttachmentLimit{OK: true}
	}
	if size > FileMaxBytes {
		return AttachmentLimit{false, "too_large", fmt.Sprintf("📎 文件过大(%.1f MB),请控制在 30 MB 以内", float64(size)/(1024*1024))}
	}
	return AttachmentLimit{OK: true}
}

type AttachmentStore struct {
	Lifecycle              *MigrationLifecycle
	Root                   string
	Now                    func() time.Time
	Random                 func() string
	Retention, OrphanGrace time.Duration
}

func NewAttachmentStore(home string) *AttachmentStore {
	return &AttachmentStore{Root: filepath.Join(home, ".claude", "im-downloads"), Now: time.Now, Random: func() string { var b [3]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }, Retention: 24 * time.Hour, OrphanGrace: 10 * time.Minute}
}

func NewConfiguredAttachmentStore(config Config) *AttachmentStore {
	store := NewAttachmentStore(config.Home)
	if config.ConfigDir != "" {
		store.Root = filepath.Join(config.ConfigDir, "im-downloads")
	}
	return store
}

var repeatedDots = regexp.MustCompile(`\.{2,}`)

func SanitizeFilename(name string) string {
	base := filepath.Base(name)
	if base == "." && name == "" {
		base = ""
	}
	base = strings.Map(func(r rune) rune {
		if r < 32 {
			return -1
		}
		if r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, base)
	base = repeatedDots.ReplaceAllString(base, "_")
	base = protocol.TrimJavaScriptString(base)
	if base == "" {
		return "unnamed"
	}
	return base
}
func (s *AttachmentStore) ResolvePath(platform, session, name string) (string, error) {
	dir := filepath.Join(s.Root, platform, SanitizeFilename(session))
	if err := os.MkdirAll(dir, 0777); err != nil {
		return "", err
	}
	name = SanitizeFilename(name)
	candidate := filepath.Join(dir, name)
	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate, nil
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	return filepath.Join(dir, fmt.Sprintf("%s-%d-%s%s", base, s.Now().UnixMilli(), s.Random(), ext)), nil
}
func (s *AttachmentStore) Write(target string, data []byte) (err error) {
	done := s.Lifecycle.Track()
	defer func() { done(err) }()
	if err := os.MkdirAll(filepath.Dir(target), 0777); err != nil {
		return err
	}
	temp := fmt.Sprintf("%s.%d.%d.part", target, os.Getpid(), s.Now().UnixMilli())
	if err := os.WriteFile(temp, data, 0666); err != nil {
		return err
	}
	return os.Rename(temp, target)
}

type GCSummary struct {
	Removed int   `json:"removed"`
	Bytes   int64 `json:"bytes"`
}

func (s *AttachmentStore) GC() GCSummary {
	done := s.Lifecycle.Track()
	defer done(nil)
	now := s.Now()
	result := GCSummary{}
	var walk func(string)
	walk = func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				walk(path)
				continue
			}
			if !entry.Type().IsRegular() {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			threshold := s.Retention
			if strings.HasSuffix(entry.Name(), ".part") {
				threshold = s.OrphanGrace
			}
			if now.Sub(info.ModTime()) > threshold && os.Remove(path) == nil {
				result.Removed++
				result.Bytes += info.Size()
			}
		}
	}
	walk(s.Root)
	return result
}

type LocalAttachment struct {
	Kind, Name, Path, MIME string
	Size                   int64
	Buffer                 []byte
}
type TelegramMedia struct {
	API   *TelegramAPI
	Store *AttachmentStore
	HTTP  *http.Client
}

var mediaExtension = regexp.MustCompile(`\.([^./\\]+)$`)

func AttachmentKind(mime, name string) string {
	if strings.HasPrefix(mime, "image/") {
		return "image"
	}
	if match := mediaExtension.FindStringSubmatch(name); match != nil {
		switch strings.ToLower(match[1]) {
		case "jpg", "jpeg", "png", "gif", "webp", "heic":
			return "image"
		}
	}
	return "file"
}
func (m *TelegramMedia) Download(ctx context.Context, id, session string, nameHint, mimeHint *string) (LocalAttachment, error) {
	result, err := m.API.Call(ctx, "getFile", protocol.NewJSONObject(protocol.JSONField{Name: "file_id", Value: raw(id)}))
	if err != nil {
		return LocalAttachment{}, err
	}
	file := object(result)
	filePath := file.String("file_path")
	if filePath == "" {
		return LocalAttachment{}, fmt.Errorf("[TelegramMedia] getFile returned no file_path for %s", id)
	}
	request, err := http.NewRequestWithContext(ctx, "GET", m.API.root+"/file/bot"+m.API.token+"/"+filePath, nil)
	if err != nil {
		return LocalAttachment{}, err
	}
	response, err := m.HTTP.Do(request)
	if err != nil {
		return LocalAttachment{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return LocalAttachment{}, fmt.Errorf("[TelegramMedia] fetch failed: %d %s", response.StatusCode, http.StatusText(response.StatusCode))
	}
	buffer, err := io.ReadAll(response.Body)
	if err != nil {
		return LocalAttachment{}, err
	}
	mime := response.Header.Get("Content-Type")
	if mimeHint != nil {
		mime = *mimeHint
	}
	parts := strings.Split(filePath, "/")
	name := parts[len(parts)-1]
	if name == "" {
		name = id
	}
	if nameHint != nil {
		name = *nameHint
	}
	kind := AttachmentKind(mime, name)
	target, err := m.Store.ResolvePath("telegram", session, name)
	if err != nil {
		return LocalAttachment{}, err
	}
	if err = m.Store.Write(target, buffer); err != nil {
		return LocalAttachment{}, err
	}
	if mime == "" {
		mime = "application/octet-stream"
		if kind == "image" {
			mime = "image/png"
		}
	}
	return LocalAttachment{kind, name, target, mime, int64(len(buffer)), buffer}, nil
}

type UploadSource struct {
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
	MIME string `json:"mime,omitempty"`
	Data string `json:"data,omitempty"`
}
type PendingUpload struct {
	ID     string       `json:"id"`
	Source UploadSource `json:"source"`
	Alt    string       `json:"alt,omitempty"`
}
type ImageWatcher struct {
	buffer      Text
	seen        map[string]bool
	accumulated []PendingUpload
}

var markdownImage = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)\)`)
var windowsAbsoluteImage = regexp.MustCompile(`^(?:[a-zA-Z]:[\\/]|\\\\[^\\]+\\[^\\]+)`)
var base64Image = regexp.MustCompile(`^data:([^;,]+);base64,(.+)$`)

func imageFingerprint(text string) string {
	var h uint32 = 5381
	for _, unit := range NewText(text) {
		h = (h * 33) ^ uint32(unit)
	}
	return fmt.Sprintf("%x", h)
}
func (w *ImageWatcher) Feed(chunk Text) []PendingUpload {
	if w.seen == nil {
		w.seen = map[string]bool{}
	}
	w.buffer = w.buffer.Join(chunk)
	text := w.buffer.String()
	result := []PendingUpload{}
	last := 0
	for _, match := range markdownImage.FindAllStringSubmatchIndex(text, -1) {
		alt, target := text[match[2]:match[3]], text[match[4]:match[5]]
		if strings.IndexFunc(target, protocol.IsJavaScriptWhitespace) >= 0 {
			continue
		}
		source := UploadSource{}
		switch {
		case strings.HasPrefix(target, "data:"):
			if m := base64Image.FindStringSubmatch(target); m != nil {
				source = UploadSource{Kind: "base64", MIME: m[1], Data: m[2]}
			}
		case strings.HasPrefix(target, "file://"):
			source = UploadSource{Kind: "path", Path: strings.TrimPrefix(target, "file://")}
		case strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://"):
			source = UploadSource{Kind: "url", URL: target}
		case strings.HasPrefix(target, "/") || windowsAbsoluteImage.MatchString(target):
			source = UploadSource{Kind: "path", Path: target}
		}
		if source.Kind != "" {
			id := imageFingerprint(source.Kind + ":" + target)
			if !w.seen[id] {
				w.seen[id] = true
				pending := PendingUpload{id, source, alt}
				result = append(result, pending)
				w.accumulated = append(w.accumulated, pending)
			}
		}
		last = len(NewText(text[:match[1]]))
	}
	if last > 0 {
		w.buffer = w.buffer.Slice(last, len(w.buffer))
	}
	if len(w.buffer) > 4096 {
		w.buffer = w.buffer.Slice(len(w.buffer)-2048, len(w.buffer))
	}
	return result
}
func (w *ImageWatcher) Drain() []PendingUpload { return append([]PendingUpload{}, w.accumulated...) }

type SafeImage struct {
	OK     bool   `json:"ok"`
	Buffer []byte `json:"-"`
	MIME   string `json:"mime,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func nodeBase64(data string) []byte {
	data = strings.Map(func(r rune) rune {
		if r == '-' {
			return '+'
		}
		if r == '_' {
			return '/'
		}
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '/' || r == '=' {
			return r
		}
		return -1
	}, data)
	data = strings.SplitN(data, "=", 2)[0]
	if len(data)%4 == 1 {
		data = data[:len(data)-1]
	}
	result, _ := base64.RawStdEncoding.DecodeString(data)
	return result
}
func LoadSafeOutboundImage(pending PendingUpload, work string, configDirs ...string) SafeImage {
	fail := func(reason string) SafeImage { return SafeImage{Reason: reason} }
	if work == "" {
		return fail("missing session work directory")
	}
	if pending.Source.Kind == "url" {
		return fail("remote image URLs are not fetched from Agent output")
	}
	if pending.Source.Kind == "base64" {
		check := CheckAttachmentLimit("image", int64((len(NewText(pending.Source.Data))*3+3)/4), pending.Source.MIME)
		if !check.OK {
			return fail(check.Hint)
		}
		buffer := nodeBase64(pending.Source.Data)
		if check = CheckAttachmentLimit("image", int64(len(buffer)), pending.Source.MIME); !check.OK {
			return fail(check.Hint)
		}
		return SafeImage{OK: true, Buffer: buffer, MIME: pending.Source.MIME}
	}
	root, err := filepath.EvalSymlinks(work)
	if err != nil {
		return fail("image path does not exist")
	}
	inputPath := pending.Source.Path
	if len(configDirs) > 0 && configDirs[0] != "" {
		inputPath, err = storagerelocations.Resolve(inputPath, configDirs[0])
		if err != nil {
			return fail(err.Error())
		}
	}
	path, err := filepath.EvalSymlinks(inputPath)
	if err != nil {
		return fail("image path does not exist")
	}
	if !within(path, root) {
		return fail("image path is outside the active session work directory")
	}
	mime := pending.Source.MIME
	if mime == "" {
		mime = "image/png"
	}
	file, err := os.Open(path)
	if err != nil {
		return fail(err.Error())
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return fail(err.Error())
	}
	if !stat.Mode().IsRegular() {
		return fail("image path is not a regular file")
	}
	check := CheckAttachmentLimit("image", stat.Size(), mime)
	if !check.OK {
		return fail(check.Hint)
	}
	buffer, err := io.ReadAll(io.LimitReader(file, ImageMaxBytes+1))
	if err != nil {
		return fail(err.Error())
	}
	if len(buffer) > ImageMaxBytes {
		return fail("image exceeded the 10 MB limit while reading")
	}
	return SafeImage{OK: true, Buffer: buffer, MIME: mime}
}
func AttachmentRef(attachment LocalAttachment) protocol.JSONObject {
	ref := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw(attachment.Kind)}, protocol.JSONField{Name: "name", Value: raw(attachment.Name)}, protocol.JSONField{Name: "mimeType", Value: raw(attachment.MIME)})
	if attachment.Kind == "image" {
		ref.Set("data", raw(base64.StdEncoding.EncodeToString(attachment.Buffer)))
	} else {
		ref.Set("path", raw(attachment.Path))
	}
	return ref
}
func collectJSONArray(v json.RawMessage) []protocol.JSONObject {
	var result []protocol.JSONObject
	_ = json.Unmarshal(v, &result)
	return result
}
