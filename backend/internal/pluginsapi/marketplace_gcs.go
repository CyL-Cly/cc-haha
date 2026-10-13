package pluginsapi

import (
	"archive/zip"
	"bytes"
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const officialMarketplaceGCS = "https://downloads.claude.ai/claude-code-releases/plugins/claude-plugins-official"
const officialArchivePrefix = "marketplaces/claude-plugins-official/"

func (r *MarketplaceResolver) officialGCS(ctx context.Context, location string) bool {
	base := filepath.Clean(filepath.Join(r.options.PluginsDir, "marketplaces"))
	resolved := filepath.Clean(location)
	if resolved != base && !strings.HasPrefix(resolved, base+string(filepath.Separator)) {
		return false
	}
	get := func(target string, timeout time.Duration) ([]byte, error) {
		next, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		req, err := http.NewRequestWithContext(next, "GET", target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("User-Agent", "axios/1.15.2")
		req.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
		response, err := r.options.Client.Do(req)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, os.ErrInvalid
		}
		return webfetch.ReadAxiosResponseBody(response)
	}
	latest, err := get(officialMarketplaceGCS+"/latest", 10*time.Second)
	if err != nil {
		return false
	}
	sha := strings.TrimSpace(string(protocol.DecodeJavaScriptUTF8(latest)))
	if sha == "" {
		return false
	}
	if current, err := os.ReadFile(filepath.Join(location, ".gcs-sha")); err == nil && strings.TrimSpace(string(current)) == sha {
		return true
	}
	wire, err := get(officialMarketplaceGCS+"/"+sha+".zip", 60*time.Second)
	if err != nil {
		return false
	}
	archive, err := zip.NewReader(bytes.NewReader(wire), int64(len(wire)))
	if err != nil {
		return false
	}
	staging := location + ".staging"
	if err = os.RemoveAll(staging); err != nil {
		return false
	}
	if err = os.MkdirAll(staging, 0777); err != nil {
		return false
	}
	for _, file := range archive.File {
		if !strings.HasPrefix(file.Name, officialArchivePrefix) {
			continue
		}
		relative := strings.TrimPrefix(file.Name, officialArchivePrefix)
		if relative == "" || strings.HasSuffix(relative, "/") {
			continue
		}
		target := filepath.Join(staging, relative)
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(staging)+string(filepath.Separator)) {
			return false
		}
		reader, err := file.Open()
		if err != nil {
			return false
		}
		data, readErr := io.ReadAll(reader)
		_ = reader.Close()
		if readErr != nil {
			return false
		}
		if err = os.MkdirAll(filepath.Dir(target), 0777); err != nil {
			return false
		}
		if err = os.WriteFile(target, data, 0666); err != nil {
			return false
		}
		if file.Mode().Perm()&0111 != 0 {
			_ = os.Chmod(target, file.Mode().Perm()&0777)
		}
	}
	if err = os.WriteFile(filepath.Join(staging, ".gcs-sha"), []byte(sha), 0666); err != nil {
		return false
	}
	if err = os.RemoveAll(location); err != nil {
		return false
	}
	return os.Rename(staging, location) == nil
}
