package mcpb

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginzip"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type Options struct {
	Home, Platform string
	Environment    func(string) (string, bool)
	HTTPClient     *http.Client
	Now            func() time.Time
	LoadUserConfig func(context.Context, string, string) (*protocol.JSONObject, error)
	SaveUserConfig func(context.Context, string, string, protocol.JSONObject, protocol.JSONObject) error
}
type Service struct{ options Options }
type Result struct {
	NeedsConfig                                    bool
	Manifest, Config, ConfigSchema, ExistingConfig protocol.JSONObject
	ExtractedPath, ContentHash                     string
	ValidationErrors                               []string
}
type metadata struct {
	Source        string `json:"source"`
	ContentHash   string `json:"contentHash"`
	ExtractedPath string `json:"extractedPath"`
	CachedAt      string `json:"cachedAt"`
	LastChecked   string `json:"lastChecked"`
}

func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.Home) || o.Platform == "" || o.Environment == nil || o.LoadUserConfig == nil || o.SaveUserConfig == nil {
		return nil, errors.New("MCPB requires explicit home/platform/environment and shared user-configuration producers")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{}
	}
	return &Service{options: o}, nil
}
func IsSource(source string) bool {
	return strings.HasSuffix(source, ".mcpb") || strings.HasSuffix(source, ".dxt")
}
func isURL(source string) bool {
	return strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://")
}
func sourceHash(source string) string {
	sum := md5.Sum([]byte(source))
	return hex.EncodeToString(sum[:])[:8]
}
func metadataPath(pluginPath, source string) string {
	return filepath.Join(pluginPath, ".mcpb-cache", sourceHash(source)+".metadata.json")
}
func loadMetadata(pluginPath, source string) *metadata {
	data, err := os.ReadFile(metadataPath(pluginPath, source))
	if err != nil {
		return nil
	}
	var m metadata
	if json.Unmarshal(protocol.DecodeJavaScriptUTF8(data), &m) != nil {
		return nil
	}
	return &m
}
func (s *Service) CheckChanged(ctx context.Context, source, pluginPath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m := loadMetadata(pluginPath, source)
	if m == nil {
		return true, nil
	}
	if _, err := os.Stat(m.ExtractedPath); err != nil {
		return true, nil
	}
	if !isURL(source) {
		info, err := os.Stat(filepath.Join(pluginPath, source))
		if err != nil {
			return true, nil
		}
		cached, err := time.Parse(time.RFC3339Nano, m.CachedAt)
		if err == nil && info.ModTime().UnixMilli() > cached.UnixMilli() {
			return true, nil
		}
	}
	return false, nil
}
func (s *Service) saveMetadata(pluginPath, source string, result Result) error {
	stamp := func() string { return s.options.Now().UTC().Format("2006-01-02T15:04:05.000Z") }
	m := metadata{source, result.ContentHash, result.ExtractedPath, stamp(), stamp()}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	path := metadataPath(pluginPath, source)
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0666)
}
func (s *Service) SystemDirectories() protocol.JSONObject {
	home := s.options.Home
	base := home
	if s.options.Platform == "win32" || s.options.Platform == "windows" {
		if value, _ := s.options.Environment("USERPROFILE"); value != "" {
			base = value
		}
	}
	result := protocol.NewJSONObject(protocol.JSONField{Name: "HOME", Value: quote(home)})
	for _, dir := range []struct{ name, folder, env string }{{"DESKTOP", "Desktop", "XDG_DESKTOP_DIR"}, {"DOCUMENTS", "Documents", "XDG_DOCUMENTS_DIR"}, {"DOWNLOADS", "Downloads", "XDG_DOWNLOAD_DIR"}} {
		value := filepath.Join(base, dir.folder)
		if s.options.Platform == "linux" || s.options.Platform == "wsl" {
			if override, _ := s.options.Environment(dir.env); override != "" {
				value = override
			}
		}
		result.Set(dir.name, quote(value))
	}
	return result
}
func (s *Service) finish(ctx context.Context, result Result, pluginID string, provided *protocol.JSONObject, force bool, onGenerate func(string)) (Result, error) {
	result.ValidationErrors = []string{}
	user := protocol.NewJSONObject()
	schema := object(result.Manifest.Get("user_config"))
	if len(schema.OwnProperties()) > 0 {
		saved, err := s.options.LoadUserConfig(ctx, pluginID, result.Manifest.String("name"))
		if err != nil {
			return Result{}, err
		}
		if saved != nil {
			user = saved.Clone()
		}
		result.ExistingConfig = user.Clone()
		if provided != nil {
			user = provided.Clone()
		}
		valid, failures := ValidateUserConfig(user, schema)
		if force || !valid {
			result.NeedsConfig = true
			result.ConfigSchema = schema
			result.ValidationErrors = failures
			return result, nil
		}
		if provided != nil {
			if err = s.options.SaveUserConfig(ctx, pluginID, result.Manifest.String("name"), provided.Clone(), schema); err != nil {
				return Result{}, err
			}
		}
	}
	progress(onGenerate, "Generating MCP server configuration...")
	config, err := GenerateConfig(result.Manifest, ConfigOptions{Platform: s.options.Platform, ExtensionPath: result.ExtractedPath, PathSeparator: "/", SystemDirectories: s.SystemDirectories(), UserConfig: user})
	if err != nil {
		return Result{}, err
	}
	if config == nil {
		return Result{}, fmt.Errorf("Failed to generate MCP server configuration from manifest \"%s\"", result.Manifest.String("name"))
	}
	result.Config = *config
	return result, nil
}
func progress(callback func(string), message string) {
	if callback != nil {
		callback(message)
	}
}
func textBytes(data []byte) []byte {
	return bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(data), []byte{0xef, 0xbb, 0xbf})
}
func (s *Service) extract(ctx context.Context, entries []pluginzip.Entry, data []byte, path string, onProgress func(string)) error {
	progress(onProgress, "Extracting files...")
	if err := os.MkdirAll(path, 0755); err != nil {
		return err
	}
	modes := pluginzip.ParseModes(data)
	count, total := 0, 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name, "/") {
			total++
		}
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasSuffix(e.Name, "/") {
			continue
		}
		full := filepath.Join(path, e.Name)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			return err
		}
		content := e.Data
		for _, suffix := range []string{".json", ".js", ".ts", ".txt", ".md", ".yml", ".yaml"} {
			if strings.HasSuffix(e.Name, suffix) {
				content = textBytes(content)
				break
			}
		}
		if err := os.WriteFile(full, content, 0666); err != nil {
			return err
		}
		if mode := modes[e.Name]; mode != 0 && mode&0111 != 0 {
			_ = os.Chmod(full, os.FileMode(mode&0777))
		}
		count++
		if count%10 == 0 {
			progress(onProgress, fmt.Sprintf("Extracted %d/%d files", count, total))
		}
	}
	progress(onProgress, fmt.Sprintf("Extraction complete (%d files)", count))
	return nil
}
func (s *Service) download(ctx context.Context, url, path string, onProgress func(string)) (data []byte, err error) {
	progress(onProgress, "Downloading "+url+"...")
	defer func() {
		if err != nil && ctx.Err() == nil {
			err = fmt.Errorf("Failed to download MCPB file from %s: %s", url, strings.TrimPrefix(storage.JavaScriptErrorText(err), "Error: "))
		}
	}()
	client := *s.options.HTTPClient
	client.Timeout = 120 * time.Second
	client.CheckRedirect = func(_ *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("Maximum number of redirects exceeded")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Request failed with status code %d", response.StatusCode)
	}
	var buf bytes.Buffer
	chunk := make([]byte, 32*1024)
	for {
		n, e := response.Body.Read(chunk)
		if n > 0 {
			_, _ = buf.Write(chunk[:n])
			if response.ContentLength > 0 {
				percent := int(math.Floor(float64(buf.Len())/float64(response.ContentLength)*100 + 0.5))
				progress(onProgress, fmt.Sprintf("Downloading... %d%%", percent))
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
	}
	data = buf.Bytes()
	if err = os.WriteFile(path, data, 0666); err != nil {
		return nil, err
	}
	progress(onProgress, "Download complete")
	return data, nil
}
func (s *Service) Load(ctx context.Context, source, pluginPath, pluginID string, onProgress func(string), provided *protocol.JSONObject, force bool) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	cache := filepath.Join(pluginPath, ".mcpb-cache")
	if err := os.MkdirAll(cache, 0755); err != nil {
		return Result{}, err
	}
	m := loadMetadata(pluginPath, source)
	changed, err := s.CheckChanged(ctx, source, pluginPath)
	if err != nil {
		return Result{}, err
	}
	if m != nil && !changed {
		path := filepath.Join(m.ExtractedPath, "manifest.json")
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return Result{}, fmt.Errorf("Cached manifest not found: %s", path)
		}
		if err != nil {
			return Result{}, err
		}
		manifest, err := ParseManifest(data)
		if err != nil {
			return Result{}, err
		}
		return s.finish(ctx, Result{Manifest: manifest, ExtractedPath: m.ExtractedPath, ContentHash: m.ContentHash}, pluginID, provided, force, nil)
	}
	var data []byte
	if isURL(source) {
		data, err = s.download(ctx, source, filepath.Join(cache, sourceHash(source)+".mcpb"), onProgress)
	} else {
		progress(onProgress, "Loading "+source+"...")
		path := filepath.Join(pluginPath, source)
		data, err = os.ReadFile(path)
		if os.IsNotExist(err) {
			err = fmt.Errorf("MCPB file not found: %s", path)
		}
	}
	if err != nil {
		return Result{}, err
	}
	hash := sha256.Sum256(data)
	contentHash := hex.EncodeToString(hash[:])[:16]
	progress(onProgress, "Extracting MCPB archive...")
	entries, err := pluginzip.Unzip(data, s.options.Platform)
	if err != nil {
		return Result{}, err
	}
	var manifestData []byte
	found := false
	for _, e := range entries {
		if e.Name == "manifest.json" {
			manifestData = e.Data
			found = true
		}
	}
	if !found {
		return Result{}, errors.New("No manifest.json found in MCPB file")
	}
	manifest, err := ParseManifest(manifestData)
	if err != nil {
		return Result{}, err
	}
	result := Result{Manifest: manifest, ContentHash: contentHash, ExtractedPath: filepath.Join(cache, contentHash)}
	if err = s.extract(ctx, entries, data, result.ExtractedPath, onProgress); err != nil {
		return Result{}, err
	}
	// Original forces the dialog only on cached loads. The fresh load first
	// validates configuration and still records extraction when it is missing.
	result, err = s.finish(ctx, result, pluginID, provided, false, onProgress)
	if err != nil {
		return Result{}, err
	}
	if err = s.saveMetadata(pluginPath, source, result); err != nil {
		return Result{}, err
	}
	return result, nil
}

func (r Result) JSONObject() protocol.JSONObject {
	result := protocol.NewJSONObject()
	if r.NeedsConfig {
		result.Set("status", quote("needs-config"))
		result.Set("manifest", encode(r.Manifest))
		result.Set("extractedPath", quote(r.ExtractedPath))
		result.Set("contentHash", quote(r.ContentHash))
		result.Set("configSchema", encode(r.ConfigSchema))
		result.Set("existingConfig", encode(r.ExistingConfig))
		result.Set("validationErrors", encode(r.ValidationErrors))
	} else {
		result.Set("manifest", encode(r.Manifest))
		result.Set("mcpConfig", encode(r.Config))
		result.Set("extractedPath", quote(r.ExtractedPath))
		result.Set("contentHash", quote(r.ContentHash))
	}
	return result
}
