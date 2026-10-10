package httphook

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestActualSourceSandboxEnabledBeforeInitializationUsesDirectHTTPHook(t *testing.T) {
	var direct, proxy atomic.Int32
	body := `{"session_id":"outside","name":"real"}`
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct.Add(1)
		b, e := io.ReadAll(r.Body)
		if e != nil || string(b) != body {
			t.Error("original physical body", string(b), e)
		}
		io.WriteString(w, `{"original":"direct"}`)
	}))
	defer target.Close()
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxy.Add(1); w.WriteHeader(403) }))
	defer trap.Close()
	fixture, _ := filepath.Abs("testdata/sandbox-pre-cli.fixture.ts")
	input, _ := json.Marshal(map[string]any{"url": target.URL, "configuredProxyPort": trap.Listener.Addr().(*net.TCPAddr).Port, "body": body})
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=/opt/homebrew/bin:/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var original struct {
		Waits, Ports int
		Initialized  bool
		Proxy        json.RawMessage
		Result       Result
	}
	if e = json.Unmarshal(out, &original); e != nil {
		t.Fatal(e, string(out))
	}
	if original.Initialized || string(original.Proxy) != "null" || original.Waits != 2 || original.Ports != 2 || !original.Result.OK || string(original.Result.Body) != `"{\"original\":\"direct\"}"` || direct.Load() != 1 || proxy.Load() != 0 {
		t.Fatal("original uninitialized Sandbox state/transport", string(out), direct.Load(), proxy.Load())
	}
}
