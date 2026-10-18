package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"golang.org/x/net/websocket"
)

func isolatedMediaOptions(t *testing.T, origin string) Options {
	o := nativeSessionOptions(t, origin)
	o.Environment["DISABLE_TELEMETRY"] = "1"
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("isolated media Keychain unavailable")
	}
	o.Native.ImageLibraryPath, _ = filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	disabled := false
	o.Arguments.ComputerUse = &disabled
	o.Arguments.Tools = nil
	return o
}
func fixtureMediaPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 17, 11))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestActualDesktopWebSocketImageDefaultCatalogAndColdResume(t *testing.T) {
	encoded := fixtureMediaPNG(t)
	base64data := base64.StdEncoding.EncodeToString(encoded)
	var mu sync.Mutex
	bodies := []json.RawMessage{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		response(w, "actual-image", "", nil, "DESKTOP_NATIVE_IMAGE_OK")
	}))
	defer peer.Close()
	o := isolatedMediaOptions(t, peer.URL)
	s, err := server.New(server.Options{HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Factory: DesktopFactory(o)})
	if err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(s)
	defer host.Close()
	defer s.Close(context.Background())
	res, err := host.Client().Post(host.URL+"/api/sessions", "application/json", bytes.NewReader(raw(map[string]string{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"})))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	sid := obj(data).String("sessionId")
	if res.StatusCode != 201 || sid == "" {
		t.Fatal(res.StatusCode, string(data))
	}
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+sid, "", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = ws.SetDeadline(time.Now().Add(20 * time.Second))
	if err := websocket.Message.Send(ws, string(raw(map[string]any{"type": "user_message", "content": "Inspect the isolated PNG.", "attachments": []any{map[string]any{"type": "image", "name": "private.png", "mimeType": "image/png", "data": "data:image/png;base64," + base64data}}}))); err != nil {
		t.Fatal(err)
	}
	completed := false
	for {
		var frame string
		if err := websocket.Message.Receive(ws, &frame); err != nil {
			t.Fatal(err)
		}
		value := obj([]byte(frame))
		if value.String("type") == "error" {
			t.Fatal(frame)
		}
		if value.String("type") == "message_complete" {
			completed = true
			requestDesktopSessionSync(t, ws)
		}
		if completed && value.String("type") == "session_state" {
			assertDesktopIdleSnapshot(t, value)
			break
		}
	}
	_ = ws.Close()
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	var path string
	_ = filepath.WalkDir(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(p string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && filepath.Base(p) == sid+".jsonl" {
			path = p
		}
		return nil
	})
	before, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(before, []byte(base64data)) || !bytes.Contains(before, []byte("DESKTOP_NATIVE_IMAGE_OK")) {
		t.Fatal("actual desktop image canonical missing", err)
	}
	staged, err := filepath.Glob(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "uploads", sid, "*-private.png"))
	if err != nil || len(staged) != 1 {
		t.Fatal("actual desktop staged image", staged, err)
	}
	if data, err := os.ReadFile(staged[0]); err != nil || !bytes.Equal(data, encoded) {
		t.Fatal("staged image bytes", err)
	}
	o.Native = nil
	o.NativeFactory = func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		n, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: filepath.Join(filepath.Dir(o.Environment["HOME"]), "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
		if err == nil {
			n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
				return nil, errors.New("isolated resumed media Keychain unavailable")
			}
			n.ImageLibraryPath, _ = filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
		}
		return n, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: sid, WorkDir: o.WorkDir, Resume: true}, func(json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close(context.Background())
	if err := live.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "Continue from the actual attached image."})); err != nil {
		t.Fatal(err)
	}
	if err := live.Close(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(after, before) {
		t.Fatal("cold desktop resume changed image prefix", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatal("actual default Harness calls", len(bodies))
	}
	for i, body := range bodies {
		if !bytes.Contains(body, []byte(base64data)) || !bytes.Contains(body, []byte(`"media_type":"image/png"`)) {
			t.Fatalf("actual request%d lost image", i)
		}
		var request protocol.JSONObject
		_ = request.UnmarshalJSON(body)
		var tools []protocol.JSONObject
		_ = json.Unmarshal(request.Get("tools"), &tools)
		if len(tools) < 19 {
			t.Fatalf("default catalog replaced with fixture tools: %d", len(tools))
		}
	}
	if !bytes.Contains(bodies[1], []byte("DESKTOP_NATIVE_IMAGE_OK")) {
		t.Fatal("cold desktop resume lost actual reply")
	}
}

func TestActualCLIStreamJSONImageUsesPublicInputPipeline(t *testing.T) {
	data := base64.StdEncoding.EncodeToString(fixtureMediaPNG(t))
	var observed json.RawMessage
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		observed, _ = io.ReadAll(r.Body)
		response(w, "actual-cli-image", "", nil, "CLI_NATIVE_IMAGE_OK")
	}))
	defer peer.Close()
	o := isolatedMediaOptions(t, peer.URL)
	input := raw(map[string]any{"type": "user", "uuid": "99999999-9999-4999-8999-999999999919", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": data}}}}})
	var output, diagnostics bytes.Buffer
	o.Stderr = &diagnostics
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	code, err := RunCommand(ctx, Command{Print: true, InputFormat: "stream-json", OutputFormat: "stream-json", Verbose: true, Arguments: o.Arguments}, o, bytes.NewReader(append(input, '\n')), &output, nil)
	if err != nil || code != 0 {
		t.Fatal(code, err, diagnostics.String(), output.String())
	}
	if !bytes.Contains(observed, []byte(data)) || !bytes.Contains(output.Bytes(), []byte("CLI_NATIVE_IMAGE_OK")) {
		t.Fatal("actual CLI media failed")
	}
	var result protocol.JSONObject
	for _, line := range bytes.Split(output.Bytes(), []byte("\n")) {
		var record protocol.JSONObject
		_ = record.UnmarshalJSON(line)
		if record.String("type") == "result" {
			result = record
		}
	}
	if result.String("subtype") != "success" || string(result.Get("is_error")) != "false" {
		t.Fatal("actual CLI final result", string(raw(result)))
	}
}
