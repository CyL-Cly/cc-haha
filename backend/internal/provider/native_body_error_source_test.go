package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeBodySocketFaultMatchesActualBunPhysicalFetch(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	for _, chunked := range []bool{false, true} {
		t.Run(fmt.Sprint(chunked), func(t *testing.T) {
			const payload = "ACTUAL_BODY_SOCKET_BYTES_186B"
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				connection, _, e := w.(http.Hijacker).Hijack()
				if e != nil {
					t.Error(e)
					return
				}
				defer connection.Close()
				framing := "Content-Length: 100000\r\n"
				if chunked {
					framing = "Transfer-Encoding: chunked\r\n"
				}
				_, _ = io.WriteString(connection, "HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\n"+framing+"\r\n")
				time.Sleep(10 * time.Millisecond)
				data := payload
				if chunked {
					data = fmt.Sprintf("%x\r\n%s\r\n", len(payload), payload)
				}
				_, _ = io.WriteString(connection, data)
				time.Sleep(30 * time.Millisecond)
			}))
			defer peer.Close()
			home := t.TempDir()
			config, temp := filepath.Join(home, ".claude"), filepath.Join(home, "tmp")
			for _, p := range []string{config, temp} {
				if e := os.MkdirAll(p, 0700); e != nil {
					t.Fatal(e)
				}
			}
			path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-body-socket.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_BODY_SOCKET_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
			input, _ := json.Marshal(map[string]any{"homeDir": home, "origin": peer.URL})
			cmd.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, e := cmd.Output()
			if e != nil {
				t.Fatalf("actual Source %v %.2400s", e, stderr.String())
			}
			var source struct {
				Bytes, Status int
				Fault         struct{ Name, Message, Code string }
			}
			if e = json.Unmarshal(out, &source); e != nil {
				t.Fatal(e)
			}
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, peer.URL, nil)
			exchange, e := NativeDirectHTTPExchangeFromEnvironment(peer.URL, map[string]string{})
			if e != nil {
				t.Fatal(e)
			}
			response, e := exchange.Do(request)
			if e != nil {
				t.Fatal(e)
			}
			data, failure := io.ReadAll(response.Response.Body)
			_ = response.Response.Body.Close()
			mapped := nativeBodyReadError(failure)
			native, ok := mapped.(*NativeStreamReadError)
			if !ok || source.Fault.Name != "Error" || native.Error() != source.Fault.Message || native.Code() != source.Fault.Code || len(data) != source.Bytes || string(data) != payload || source.Status != 200 {
				t.Fatalf("native physical socket differs: Go %d/%v Source %s", len(data), mapped, out)
			}
		})
	}
}
