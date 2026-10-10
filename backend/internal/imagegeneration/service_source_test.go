package imagegeneration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestImageServiceActualSourcePhysicalHTTPFilesGenerateEditAndErrors(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	png := base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0})
	rows := []map[string]any{}
	for _, kind := range []string{"openai_images", "openai_oauth", "grok_oauth"} {
		for _, edit := range []bool{false, true} {
			for _, count := range []int{1, 2} {
				config := map[string]any{"kind": kind, "providerId": "private-images", "model": "private-image-model", "baseUrl": "https://relay.invalid/v1", "apiKey": "private-fake-key"}
				body := string(encoded(map[string]any{"data": []map[string]string{{"b64_json": png, "revised_prompt": " private revised "}}}))
				if kind == "openai_oauth" {
					body = "data: " + string(encoded(map[string]any{"item": map[string]any{"type": "image_generation_call", "result": png, "output_format": "png", "revised_prompt": " private revised "}})) + "\n\ndata: [DONE]\n"
				}
				rows = append(rows, map[string]any{"config": config, "edit": edit, "inputBytes": png, "input": map[string]any{"prompt": "A private paper-cut fox", "count": count, "aspect_ratio": "9:16", "quality": "high"}, "tokens": map[string]any{"accessToken": "private-fake-token", "accountId": "private-account"}, "responses": []map[string]any{{"status": 200, "body": body}}})
			}
		}
	}
	base := map[string]any{"kind": "openai_images", "providerId": "private", "model": "private-image", "baseUrl": "https://relay.invalid/v1", "apiKey": "private-secret"}
	for _, response := range []map[string]any{{"status": 401, "body": "private-secret Bearer hidden-token sk-unwanted\n\tprivate failure"}, {"status": 200, "body": `{"data":[]}`}, {"status": 200, "body": `{"data":[{"b64_json":"bm90LWltYWdl"}]}`}, {"status": 200, "body": `{"data":[{"url":"https://other.invalid/a.png"}]}`}} {
		rows = append(rows, map[string]any{"config": base, "input": map[string]any{"prompt": "failure", "count": 1}, "responses": []map[string]any{response}})
	}
	rows = append(rows, map[string]any{"config": map[string]any{"kind": "openai_oauth", "providerId": "private", "model": "gpt-image-2"}, "input": map[string]any{"prompt": "missing login", "count": 1}, "responses": []map[string]any{{"status": 200, "body": "unused"}}})
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-imagegen-service.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_IMAGEGEN_SERVICE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(encoded(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source image service: %v %s", err, stderr.String())
	}
	var expected []json.RawMessage
	if json.Unmarshal(data, &expected) != nil || len(expected) != len(rows) {
		t.Fatalf("Source service shape: %s", data)
	}
	for i, row := range rows {
		root := t.TempDir()
		inputDir := filepath.Join(root, "inputs")
		outputDir := filepath.Join(root, "output")
		if err := os.Mkdir(inputDir, 0700); err != nil {
			t.Fatal(err)
		}
		input := inputObject(row["input"])
		if row["edit"] == true {
			source := filepath.Join(inputDir, "source.png")
			pngBytes, _ := base64.StdEncoding.DecodeString(png)
			if err := os.WriteFile(source, pngBytes, 0600); err != nil {
				t.Fatal(err)
			}
			input.Set("referenced_image_paths", encoded([]string{source}))
		}
		requests := []any{}
		responses := row["responses"].([]map[string]any)
		var current int
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response := responses[current]
			w.WriteHeader(response["status"].(int))
			_, _ = io.WriteString(w, response["body"].(string))
		}))
		options := Options{Files: NativeFiles{}, Environment: func() map[string]string { return map[string]string{} }, ConfigHome: root, SessionID: func() string { return "private-session" }, InputRoots: func() []string { return []string{inputDir} }, OutputDir: outputDir, NowMillis: func() int64 { return 1770000000000 }, UUID: func() string { return "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" }}
		options.OpenAITokens = func(context.Context) (*protocol.JSONObject, error) {
			if row["tokens"] == nil {
				return nil, nil
			}
			o := inputObject(row["tokens"])
			return &o, nil
		}
		options.GrokTokens = func(context.Context, bool) (*protocol.JSONObject, error) {
			if row["tokens"] == nil {
				return nil, nil
			}
			o := inputObject(row["tokens"])
			return &o, nil
		}
		options.Fetch = func(ctx context.Context, request Request) (Response, error) {
			current = len(requests)
			if current >= len(responses) {
				current = len(responses) - 1
			}
			r, err := http.NewRequestWithContext(ctx, request.Method, peer.URL, bytes.NewReader(request.Body))
			if err != nil {
				return Response{}, err
			}
			r.Header = request.Headers.Clone()
			response, err := http.DefaultClient.Do(r)
			if err != nil {
				return Response{}, err
			}
			defer response.Body.Close()
			raw, e := io.ReadAll(response.Body)
			keys := []string{}
			for key := range request.Headers {
				keys = append(keys, strings.ToLower(key))
			}
			sort.Strings(keys)
			headers := [][]string{}
			for _, key := range keys {
				headers = append(headers, []string{key, request.Headers.Get(key)})
			}
			var body any
			if strings.HasPrefix(request.Headers.Get("Content-Type"), "multipart/") {
				_, params, e := mime.ParseMediaType(request.Headers.Get("Content-Type"))
				if e != nil {
					return Response{}, e
				}
				reader := multipart.NewReader(bytes.NewReader(request.Body), params["boundary"])
				entries := []any{}
				for {
					part, e := reader.NextPart()
					if e == io.EOF {
						break
					}
					if e != nil {
						return Response{}, e
					}
					b, e := io.ReadAll(part)
					if e != nil {
						return Response{}, e
					}
					var value any = string(b)
					if part.FileName() != "" {
						value = map[string]any{"name": part.FileName(), "type": part.Header.Get("Content-Type"), "bytes": base64.StdEncoding.EncodeToString(b)}
					}
					entries = append(entries, []any{part.FormName(), value})
				}
				body = entries
			} else {
				_ = json.Unmarshal(request.Body, &body)
			}
			// Fetch sets multipart Content-Type itself; the original captured init
			// has no Content-Type header before native FormData serialization.
			if strings.HasPrefix(request.Headers.Get("Content-Type"), "multipart/") {
				filtered := [][]string{}
				for _, h := range headers {
					if h[0] != "content-type" {
						filtered = append(filtered, h)
					}
				}
				headers = filtered
			}
			requests = append(requests, map[string]any{"url": request.URL, "method": request.Method, "headers": headers, "body": body})
			return Response{Status: response.StatusCode, Headers: response.Header, Body: raw, ReadError: e}, nil
		}
		service, err := New(options)
		if err != nil {
			t.Fatal(err)
		}
		var config Config
		_ = json.Unmarshal(encoded(row["config"]), &config)
		output, err := service.Generate(context.Background(), input, config)
		peer.Close()
		actual := map[string]any{"requests": requests, "refreshes": 0}
		if err != nil {
			actual["error"] = err.Error()
			files, _ := os.ReadDir(outputDir)
			names := []string{}
			for _, file := range files {
				names = append(names, file.Name())
			}
			actual["files"] = names
		} else {
			fileRows := []any{}
			for j, image := range output.Images {
				raw, err := os.ReadFile(image.Path)
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(image.Path)
				if err != nil {
					t.Fatal(err)
				}
				path := strings.Replace(image.Path, root, "$ROOT", 1)
				fileRows = append(fileRows, map[string]any{"path": path, "bytes": base64.StdEncoding.EncodeToString(raw), "mode": uint32(info.Mode().Perm())})
				output.Images[j].Path = path
			}
			actual["value"] = output
			actual["files"] = fileRows
		}
		var got, want any
		_ = json.Unmarshal(encoded(actual), &got)
		_ = json.Unmarshal(expected[i], &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("row%d Go%s\nSource%s", i, encoded(actual), expected[i])
		}
	}
}
