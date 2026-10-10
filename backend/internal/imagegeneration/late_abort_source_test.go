package imagegeneration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestImageOutputFilesAfterLateAbortMatchActualSource(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-fake-key" {
			t.Error("actual credential changed")
			http.Error(w, "unauthenticated", 401)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(png))
	}))
	defer peer.Close()
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-image-late-abort-phase.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_IMAGE_SIGNAL_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(encoded(map[string]any{"url": peer.URL + "/image"}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatal("original Source generateImages/caller.abort", e, stderr.String())
	}
	var expected []map[string]any
	if e = json.Unmarshal(data, &expected); e != nil {
		t.Fatal(e)
	}
	for i, kind := range []string{"normal", "abort-after-complete-provider-json"} {
		root := t.TempDir()
		directory := filepath.Join(root, "output")
		parent, abort := context.WithCancel(context.Background())
		service, e := New(Options{Files: NativeFiles{}, Environment: func() map[string]string { return map[string]string{} }, ConfigHome: root, OutputDir: directory, SessionID: func() string { return "private" }, NowMillis: func() int64 { return 1000 }, UUID: func() string { return "private" }, Fetch: func(s context.Context, request Request) (Response, error) {
			r, e := http.NewRequestWithContext(s, request.Method, peer.URL+"/image", bytes.NewReader(request.Body))
			if e != nil {
				return Response{}, e
			}
			r.Header = request.Headers.Clone()
			reply, e := http.DefaultClient.Do(r)
			if e != nil {
				return Response{}, e
			}
			defer reply.Body.Close()
			body, e := io.ReadAll(reply.Body)
			if e == nil && kind == "abort-after-complete-provider-json" {
				abort()
				<-s.Done()
			}
			return Response{Status: reply.StatusCode, Headers: reply.Header, Body: body, ReadError: e}, nil
		}})
		if e != nil {
			t.Fatal(e)
		}
		value, failure := service.Generate(parent, inputObject(map[string]any{"prompt": "original combined image signal", "count": 1}), Config{Kind: "openai_images", ProviderID: "private", Model: "private-image", APIKey: "private-fake-key", BaseURL: peer.URL})
		files, _ := os.ReadDir(directory)
		actual := map[string]any{"kind": kind, "images": float64(len(value.Images)), "files": float64(len(files))}
		if failure != nil {
			actual["error"] = failure.Error()
		}
		delete(expected[i], "phase")
		delete(expected[i], "aborted") // This same-area assertion concerns the output filesystem consumer; the real owner test checks combined signal lifecycle.
		if !reflect.DeepEqual(actual, expected[i]) {
			t.Fatalf("late output Source%v Go%v", expected[i], actual)
		}
		for _, file := range files {
			bytes, e := os.ReadFile(filepath.Join(directory, file.Name()))
			if e != nil || !reflect.DeepEqual(bytes, png) {
				t.Fatal("actual original output bytes", e)
			}
		}
		abort()
	}
}
