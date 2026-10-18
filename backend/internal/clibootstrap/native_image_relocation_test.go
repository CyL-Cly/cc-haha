package clibootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestRelocatedInstalledGoCLIReadsAndNormalizesActualImage(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("installed library fixture is macOS arm64; other platforms need matching packaged library")
	}
	root := t.TempDir()
	stage := filepath.Join(root, "staging", "Claude Code Haha.app")
	appRoot := filepath.Join("Contents", "Resources", "app.asar.unpacked")
	relativeBinary := filepath.Join(appRoot, "src-tauri", "binaries", "claude-go-sidecar")
	relativeLibrary := filepath.Join(appRoot, "node_modules", "@img", "sharp-libvips-darwin-arm64", "lib", "libvips-cpp.8.17.3.dylib")
	for _, directory := range []string{filepath.Dir(filepath.Join(stage, relativeBinary)), filepath.Dir(filepath.Join(stage, relativeLibrary))} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	library, err := os.ReadFile("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(stage, relativeLibrary), library, 0600); err != nil {
		t.Fatal(err)
	}
	buildCtx, stopBuild := context.WithTimeout(context.Background(), 90*time.Second)
	defer stopBuild()
	build := exec.CommandContext(buildCtx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-mod=readonly", "-o", filepath.Join(stage, relativeBinary), "./testdata/image-relocated-cli")
	capturePublicGoCoverage(t, build)
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("relocated actual CLI build: %v %s", err, data)
	}
	registerBuiltPublicGoCoverage(t, build)
	installed := filepath.Join(root, "中文 安装目录", "Claude Code Haha.app")
	if err = os.MkdirAll(filepath.Dir(installed), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(stage, installed); err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(filepath.Dir(stage)); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "中文 用户项目")
	home := filepath.Join(root, "private-home")
	config := filepath.Join(root, "private-config")
	temp := filepath.Join(root, "private-tmp")
	for _, directory := range []string{project, home, config, temp, filepath.Join(root, "managed")} {
		if err = os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	source := image.NewNRGBA(image.Rect(0, 0, 9001, 3))
	for x := 0; x < 9001; x++ {
		for y := 0; y < 3; y++ {
			source.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), uint8(x + y), 137})
		}
	}
	var original bytes.Buffer
	if err = png.Encode(&original, source); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "actual oversized image.png")
	if err = os.WriteFile(path, original.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var heads atomic.Int32
	var processed atomic.Bool
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			heads.Add(1)
			if r.Header.Get("Authorization") != "" || r.Header.Get("X-Api-Key") != "" || r.UserAgent() != "Bun/1.3.14" {
				t.Error("actual init HEAD acquired model credentials or changed Source user agent")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			t.Error("unexpected actual model method", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		switch requests.Add(1) {
		case 1:
			_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(fmt.Sprintf(`{"type":"tool_use","id":"installed-image-read","name":"Read","input":{"file_path":%q}}`, path))}))
		case 2:
			var request protocol.JSONObject
			if err := request.UnmarshalJSON(body); err != nil {
				t.Error(err)
				return
			}
			var messages []protocol.JSONObject
			_ = json.Unmarshal(request.Get("messages"), &messages)
			for _, message := range messages {
				var blocks []protocol.JSONObject
				_ = json.Unmarshal(message.Get("content"), &blocks)
				for _, block := range blocks {
					var results []protocol.JSONObject
					if block.String("type") != "tool_result" || json.Unmarshal(block.Get("content"), &results) != nil {
						continue
					}
					for _, result := range results {
						if result.String("type") != "image" {
							continue
						}
						blob, _ := result.Object("source")
						data, err := base64.StdEncoding.DecodeString(blob.String("data"))
						if err != nil {
							t.Error(err)
							continue
						}
						metadata, _, err := image.DecodeConfig(bytes.NewReader(data))
						if err == nil && metadata.Width <= 8000 && metadata.Width > 0 && !bytes.Equal(data, original.Bytes()) {
							processed.Store(true)
						}
					}
				}
			}
			if !processed.Load() {
				t.Errorf("installed native codec did not normalize actual oversized image: %.3000s", body)
			}
			_, _ = io.WriteString(w, webWire("deepseek-flash", []protocol.JSONObject{webObject(`{"type":"text","text":"INSTALLED_GO_IMAGE_READ_OK"}`)}))
		default:
			t.Error("unexpected actual CLI inference")
			http.Error(w, "unexpected", 400)
		}
	}))
	defer peer.Close()
	ca := filepath.Join(root, "private-ca.pem")
	if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(installed, relativeBinary), "--print", "--no-computer-use", "--model", "deepseek-flash", "--thinking", "disabled", "--system-prompt", "Read the actual requested image.", "--tools", "Read", "--allowedTools", "Read", "--output-format", "stream-json", "--verbose", "--max-turns", "3", "Read the actual oversized image.")
	command.Dir = project
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + temp, "USER=private-image-relocation", "CC_HAHA_IMAGE_RELOCATION_ROOT=" + root, "ANTHROPIC_API_KEY=private-fake-key", "ANTHROPIC_BASE_URL=" + peer.URL, "NODE_EXTRA_CA_CERTS=" + ca, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "CLAUDE_CODE_ATTRIBUTION_HEADER=0", "ENABLE_TOOL_SEARCH=false", "TZ=UTC"}
	capturePublicGoCoverage(t, command)
	output, err := command.CombinedOutput()
	if err != nil || requests.Load() != 2 || !processed.Load() || !bytes.Contains(output, []byte("INSTALLED_GO_IMAGE_READ_OK")) {
		t.Fatalf("relocated actual CLI result: %v requests=%d processed=%v %.7000s", err, requests.Load(), processed.Load(), output)
	}
	if heads.Load() != 1 {
		t.Fatal("relocated CLI did not execute its one actual init HEAD", heads.Load())
	}
	data, err := os.ReadFile(filepath.Join(root, "library-receipt.json"))
	var receipt map[string]string
	canonicalProject, canonicalErr := filepath.EvalSymlinks(project)
	if err != nil || canonicalErr != nil || json.Unmarshal(data, &receipt) != nil || receipt["library"] != filepath.Join(installed, relativeLibrary) || receipt["cwd"] != canonicalProject {
		t.Fatal("installed executable dependency lookup", err, string(data))
	}
	var transcript []byte
	if err = filepath.WalkDir(filepath.Join(config, "projects"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".jsonl") {
			data, err := os.ReadFile(path)
			transcript = append(transcript, data...)
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"installed-image-read", "INSTALLED_GO_IMAGE_READ_OK", `"originalWidth":9001`, `"displayWidth":8000`} {
		if !bytes.Contains(transcript, []byte(marker)) {
			t.Fatal("relocated CLI canonical transcript lacks actual image result", marker)
		}
	}
	if physical, err := os.ReadFile(path); err != nil || !bytes.Equal(physical, original.Bytes()) {
		t.Fatal("Read mutated private source image", err)
	}
}
