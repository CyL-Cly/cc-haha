package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestDesktopWhatsAppActualSourceUnknownPollAndEndpointFactory(t *testing.T) {
	home := t.TempDir()
	fixture, _ := filepath.Abs("../imregistration/testdata/whatsapp-api.fixture.ts")
	actions := []map[string]any{{"tail": "login/poll", "raw": `{"sessionKey":"unknown-private"}`}, {"tail": "login/poll", "raw": `null`}, {"tail": "login/poll", "raw": `invalid`}, {"tail": "login/cancel", "raw": `{"sessionKey":"unknown-private"}`}, {"tail": "login/start", "method": "GET"}, {"tail": "unknown", "method": "GET"}}
	for _, value := range []any{"", strings.Repeat("x", 256), strings.Repeat("x", 257), strings.Repeat("🧪", 128), strings.Repeat("🧪", 129), true, 123, []any{}, map[string]any{}} {
		raw, _ := json.Marshal(map[string]any{"sessionKey": value})
		actions = append(actions, map[string]any{"tail": "login/poll", "raw": string(raw)})
	}
	for _, raw := range []string{`{"sessionKey":"\ud800"}`, `{"sessionKey":null}`, `[]`, `{}`} {
		actions = append(actions, map[string]any{"tail": "login/poll", "raw": raw})
	}
	input, _ := json.Marshal(map[string]any{"home": home, "actions": actions})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_REGISTRATION_FIXTURE=1", "NODE_ENV=test", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, e := cmd.Output()
	if e != nil {
		t.Fatal("actual Source", e, stderr.String())
	}
	var source struct {
		Rows []struct {
			Status int
			Body   json.RawMessage
		}
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(raw, &source); e != nil || len(source.Rows) != len(actions) || len(source.Capabilities.Blocked) != 0 {
		t.Fatalf("Source %v %s", e, raw)
	}
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	s, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	peer := httptest.NewServer(s)
	defer peer.Close()
	for i, row := range actions {
		method := "POST"
		if value, ok := row["method"].(string); ok {
			method = value
		}
		req, _ := http.NewRequest(method, peer.URL+"/api/adapters/whatsapp/"+row["tail"].(string), strings.NewReader(fmtString(row["raw"])))
		res, e := peer.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(res.Body)
		res.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		response := httptest.NewRecorder()
		response.Code = res.StatusCode
		response.Body.Write(body)
		var actual, want any
		_ = json.Unmarshal(response.Body.Bytes(), &actual)
		_ = json.Unmarshal(source.Rows[i].Body, &want)
		if response.Code != source.Rows[i].Status || !reflect.DeepEqual(actual, want) {
			t.Errorf("case%d actual factory Go%d%s Source%d%s", i, response.Code, response.Body.String(), source.Rows[i].Status, source.Rows[i].Body)
		}
	}
}
func fmtString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
