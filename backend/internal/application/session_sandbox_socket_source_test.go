package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSourceSandboxProxySocketCloseRetainsHostPendingUntilAnswerOrLifetimeEnd(t *testing.T) {
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/sandbox-socket-pending.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewBufferString(`{"rows":[{"eof":false},{"eof":true}]}`)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source []map[string]any
	if e = json.Unmarshal(out, &source); e != nil || len(source) != 2 {
		t.Fatal(e, string(out))
	}
	for i, eof := range []bool{false, true} {
		t.Run(fmt.Sprintf("lifetimeEnd=%v", eof), func(t *testing.T) {
			var count atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				w.Write([]byte("must not contact disconnected target"))
			}))
			defer target.Close()
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			o.Arguments.PermissionMode = "default"
			o.Native.Environment["PATH"] = "/opt/homebrew/bin:/usr/bin:/bin"
			o.Native.Environment["CLAUDE_CODE_TMPDIR"] = o.Native.Temp
			o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
				return nil, fmt.Errorf("isolated Keychain unavailable")
			}
			config := o.Native.Environment["CLAUDE_CONFIG_DIR"]
			os.MkdirAll(config, 0700)
			os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"sandbox":{"enabled":true,"network":{"allowedDomains":[]}}}`), 0600)
			s, e := NewSession(ctx, o)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close(context.Background())
			port, _, e := s.process.NativeSandboxProxyPorts(ctx)
			if e != nil {
				t.Fatal(e)
			}
			c, e := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
			if e != nil {
				t.Fatal(e)
			}
			fmt.Fprintf(c, "GET %s/original HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n", target.URL)
			var pending []tui.PermissionRequest
			for {
				pending = s.PendingPermissions()
				if len(pending) == 1 {
					break
				}
				select {
				case <-ctx.Done():
					c.Close()
					t.Fatal("actual host request not received")
				case <-time.After(time.Millisecond):
				}
			}
			observed := map[string]any{"waiting": float64(len(pending)), "toolName": pending[0].ToolName, "host": "127.0.0.1", "description": pending[0].Description}
			c.Close()
			observed["disconnected"] = float64(len(s.PendingPermissions()))
			if !eof {
				if e = s.RespondPermission(ctx, pending[0].ID, tui.PermissionDecision{Allowed: true}); e != nil {
					t.Fatal(e)
				}
			}
			if e = s.Close(ctx); e != nil {
				t.Fatal("pending proxy failed to join administrative close", e)
			}
			observed["remaining"] = float64(len(s.PendingPermissions()))
			observed["upstreamRequests"] = float64(count.Load())
			if !reflect.DeepEqual(source[i], observed) {
				t.Fatalf("actual original HTTP socket/SKD pending %v; native %v", source[i], observed)
			}
		})
	}
}
