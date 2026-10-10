package voice

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestVoiceDownloaderActualSourceResumeRestartAndDigestFailures(t *testing.T) {
	data := []byte(strings.Repeat("original-file-", 100))
	sum := sha256.Sum256(data)
	for _, scenario := range []string{"fresh", "resume", "complete", "bad-prefix", "ignored-range", "invalid-range", "corrupt", "too-large", "http"} {
		t.Run(scenario, func(t *testing.T) {
			var mu sync.Mutex
			requests := [][2]string{}
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests = append(requests, [2]string{r.Method, r.Header.Get("Range")})
				mu.Unlock()
				if scenario == "http" {
					w.WriteHeader(404)
					return
				}
				body := data
				if scenario == "corrupt" {
					body = append([]byte{}, data...)
					body[0] ^= 1
				}
				if scenario == "too-large" {
					body = append(append([]byte{}, data...), 0)
				}
				offset := 0
				if value := strings.TrimPrefix(r.Header.Get("Range"), "bytes="); value != "" {
					offset, _ = strconv.Atoi(strings.TrimSuffix(value, "-"))
				}
				if offset > 0 && scenario != "ignored-range" {
					w.Header().Set("Content-Range", "bytes "+strconv.Itoa(offset)+"-"+strconv.Itoa(len(data)-1)+"/"+strconv.Itoa(len(data)))
					if scenario == "invalid-range" {
						w.Header().Set("Content-Range", "bad")
					}
					w.WriteHeader(206)
					_, _ = w.Write(body[offset:])
					return
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				_, _ = w.Write(body)
			}))
			defer host.Close()
			asset := Asset{"payload.bin", int64(len(data)), DownloadHash{"sha256", "hex", hex.EncodeToString(sum[:])}, []string{host.URL + "/payload"}}
			part := []byte(nil)
			switch scenario {
			case "resume", "ignored-range", "invalid-range":
				part = data[:20]
			case "complete":
				part = data
			case "bad-prefix":
				part = append([]byte{}, data[:20]...)
				part[0] ^= 1
			}
			input := map[string]any{"asset": asset}
			if part != nil {
				input["part"] = base64.StdEncoding.EncodeToString(part)
			}
			out := sourceVoice(t, "native-voice-download.fixture.ts", input, map[string]string{"CC_HAHA_VOICE_DOWNLOAD_FIXTURE": "1"})
			var source struct {
				Result          *DownloadResult
				Error           *Failure
				Completed, Part *string
			}
			if e := json.Unmarshal(out, &source); e != nil {
				t.Fatal(e)
			}
			mu.Lock()
			sourceRequests := append([][2]string{}, requests...)
			requests = nil
			mu.Unlock()
			home := t.TempDir()
			path := filepath.Join(home, "payload.bin")
			if part != nil {
				_ = os.WriteFile(path+".part", part, 0600)
			}
			zero := 0
			result, e := DownloadAsset(context.Background(), asset, path, DownloadOptions{MaxRetries: &zero}, nil)
			var actualError *Failure
			if e != nil {
				if failure, ok := e.(*DownloadError); ok {
					actualError = &failure.Failure
				} else {
					t.Fatal(e)
				}
			}
			var actualResult *DownloadResult
			if e == nil {
				actualResult = &result
			}
			compareVoice(t, "download result", actualResult, source.Result)
			compareVoice(t, "download error", actualError, source.Error)
			read := func(path string) *string {
				data, e := os.ReadFile(path)
				if e != nil {
					return nil
				}
				encoded := base64.StdEncoding.EncodeToString(data)
				return &encoded
			}
			compareVoice(t, "atomic completed", read(path), source.Completed)
			compareVoice(t, "retained part", read(path+".part"), source.Part)
			mu.Lock()
			actualRequests := append([][2]string{}, requests...)
			mu.Unlock()
			compareVoice(t, "actual HTTP requests", actualRequests, sourceRequests)
		})
	}
}
