package clibootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/imagegeneration"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func newNativeImageHarness(t *testing.T, origin string, cert []byte, output func([]byte) error) *MainPrint {
	t.Helper()
	fixture := newNativePluginFixture(t)
	fixture.write(t, filepath.Join(fixture.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	n := fixture.n
	n.Environment["ANTHROPIC_API_KEY"] = "private-image-harness-fake-key"
	n.NativeProcess = true
	n.ManagedRoot = filepath.Join(n.Temp, "managed")
	_ = os.MkdirAll(n.ManagedRoot, 0700)
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("private image fixture Keychain unavailable")
	}
	n.ResolvePolicy = func() (*protocol.JSONObject, error) { value := protocol.NewJSONObject(); return &value, nil }
	for key, value := range map[string]string{"PATH": "/usr/bin:/bin", "ANTHROPIC_BASE_URL": origin, "CLAUDE_CODE_ATTRIBUTION_HEADER": "0", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CC_HAHA_IMAGE_PROVIDER_KIND": "openai_images", "CC_HAHA_IMAGE_PROVIDER_ID": "private-images", "CC_HAHA_IMAGE_MODEL": "private-image-model", "CC_HAHA_IMAGE_BASE_URL": origin, "CC_HAHA_IMAGE_API_KEY": "private-image-key"} {
		n.Environment[key] = value
	}
	n.ImageLibraryPath, _ = filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	ca := filepath.Join(n.Temp, "image-harness-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), 0600); err != nil {
		t.Fatal(err)
	}
	n.Environment["NODE_EXTRA_CA_CERTS"] = ca
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "deepseek-flash", SystemPrompt: "Execute the requested real image tools in this private workspace.", Tools: []string{"ImageGen,ImageEdit,Read"}, AllowedTools: []string{"Read"}, MaxTurns: 6, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnOutput: output})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	if err = m.StartSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestActualSharedHarnessImageGenerateEditReadAndCanonicalResults(t *testing.T) {
	image := image.NewNRGBA(image.Rect(0, 0, 12, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 12; x++ {
			image.SetNRGBA(x, y, color.NRGBA{20, 40, 60, 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	models, images := 0, 0
	results := []imagegeneration.Output{}
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(r.URL.Path, "/images/") {
			images++
			if r.Header.Get("Authorization") != "Bearer private-image-key" {
				t.Error("actual image auth")
			}
			if images == 2 {
				if err := r.ParseMultipartForm(1 << 20); err == nil {
					t.Error("body was already consumed before parsing; fixture must inspect buffered multipart")
				}
				if !bytes.Contains(data, encoded.Bytes()) {
					t.Error("actual editor omitted generated image bytes")
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(encoded.Bytes()))
			return
		}
		models++
		var request protocol.JSONObject
		if err := request.UnmarshalJSON(data); err != nil {
			t.Error(err)
			return
		}
		var history []protocol.JSONObject
		_ = json.Unmarshal(request.Get("messages"), &history)
		var last imagegeneration.Output
		for _, message := range history {
			var blocks []protocol.JSONObject
			if json.Unmarshal(message.Get("content"), &blocks) == nil {
				for _, block := range blocks {
					if block.String("type") == "tool_result" {
						var value imagegeneration.Output
						if json.Unmarshal([]byte(block.String("content")), &value) == nil && value.Type == "image_generation_result" {
							last = value
						}
					}
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch models {
		case 1:
			if !strings.Contains(string(request.Get("tools")), `"name":"ImageGen"`) || !strings.Contains(string(request.Get("tools")), `"name":"ImageEdit"`) {
				t.Error("real Harness did not advertise image tools")
			}
			_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"tool_use","id":"real-generate","name":"ImageGen","input":{"prompt":"native private image","count":1}}`)}))
		case 2:
			if last.Operation != "generate" || len(last.Images) != 1 {
				t.Error("main continuation lacks actual ImageGen output")
				http.Error(w, "missing image", 500)
				return
			}
			results = append(results, last)
			_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(fmt.Sprintf(`{"type":"tool_use","id":"real-edit","name":"ImageEdit","input":{"prompt":"edit actual generated image","referenced_image_paths":[%q]}}`, last.Images[0].Path))}))
		case 3:
			if last.Operation != "edit" || len(last.Images) != 1 {
				t.Error("main continuation lacks actual ImageEdit output")
				http.Error(w, "missing edit", 500)
				return
			}
			results = append(results, last)
			_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(fmt.Sprintf(`{"type":"tool_use","id":"real-image-read","name":"Read","input":{"file_path":%q}}`, last.Images[0].Path))}))
		case 4:
			if !bytes.Contains(data, []byte(base64.StdEncoding.EncodeToString(encoded.Bytes()))) || !bytes.Contains(data, []byte(`"media_type":"image/png"`)) {
				t.Error("image Read was not sent as actual base64 image block")
			}
			_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"text","text":"NATIVE_IMAGE_HARNESS_OK"}`)}))
		default:
			t.Error("unexpected main inference", models)
			http.Error(w, "extra", 500)
		}
	}))
	defer peer.Close()
	var output bytes.Buffer
	m := newNativeImageHarness(t, peer.URL, peer.Certificate().Raw, func(data []byte) error { output.Write(data); return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"Generate an image, edit the returned path, and read that image."}}`)); err != nil {
		t.Fatal(err, output.String())
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if models != 4 || images != 2 || len(results) != 2 || !strings.Contains(output.String(), "NATIVE_IMAGE_HARNESS_OK") {
		t.Fatal("actual image Harness loop", models, images, len(results), output.String())
	}
	for _, result := range results {
		data, err := os.ReadFile(result.Images[0].Path)
		if err != nil || !bytes.Equal(data, encoded.Bytes()) {
			t.Fatal("physical final image", err)
		}
	}
	var transcript []byte
	_ = filepath.WalkDir(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, m.Controller.SessionID()+".jsonl") {
			transcript, _ = os.ReadFile(path)
		}
		return nil
	})
	for _, marker := range []string{"real-generate", "real-edit", "real-image-read", "NATIVE_IMAGE_HARNESS_OK"} {
		if !bytes.Contains(transcript, []byte(marker)) {
			t.Fatal("canonical transcript missing actual image loop", marker)
		}
	}
}
