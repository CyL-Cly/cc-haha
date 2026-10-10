package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSourceRuntimeDefaultAndExplicitProviderOwnNativeRoute(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun unavailable")
	}
	var seen []string
	var mu sync.Mutex
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, "first")
		mu.Unlock()
		response(w, "first-route", "", nil, "FIRST ROUTE")
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, "second")
		mu.Unlock()
		response(w, "second-route", "", nil, "SECOND ROUTE")
	}))
	defer second.Close()
	options := nativeSessionOptions(t, first.URL)
	options.Environment["DISABLE_TELEMETRY"] = "1"
	managed := options.Native.ManagedRoot
	options.NativeFactory = func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		n, e := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
		if e == nil {
			n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
				return nil, fmt.Errorf("isolated runtime Keychain unavailable")
			}
		}
		return n, e
	}
	index := map[string]any{"schemaVersion": 6, "activeId": "second", "providers": []any{map[string]any{"id": "first", "presetId": "deepseek", "name": "first route", "baseUrl": first.URL, "apiKey": "first-fake-key", "apiFormat": "anthropic", "authStrategy": "api_key", "models": map[string]string{"main": "fixture-model", "haiku": "", "sonnet": "", "opus": ""}}, map[string]any{"id": "second", "presetId": "deepseek", "name": "second route", "baseUrl": second.URL, "apiKey": "second-fake-key", "apiFormat": "anthropic", "authStrategy": "api_key", "models": map[string]string{"main": "fixture-model", "haiku": "", "sonnet": "", "opus": ""}}}}
	directory := filepath.Join(options.Environment["CLAUDE_CONFIG_DIR"], "cc-haha")
	if e := os.MkdirAll(directory, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(directory, "providers.json"), raw(index), 0600); e != nil {
		t.Fatal(e)
	}
	cases := []struct {
		Selected string `json:"selected"`
		Want     string `json:"-"`
	}{{"", "second"}, {"first", "first"}, {"second", "second"}}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	config := filepath.Join(home, ".claude")
	tmp := filepath.Join(home, "tmp")
	for _, p := range []string{home, config, tmp} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	root, _ := filepath.Abs("../../..")
	cmd := exec.Command(bun, "--no-env-file", filepath.Join(root, "scripts/quality-gate/go-parity/native-runtime-provider-selection.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + tmp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "NODE_ENV=test", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"index": index, "cases": cases})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	source, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var expected []map[string]string
	if e = json.Unmarshal(source, &expected); e != nil {
		t.Fatal(e, string(source))
	}
	for i, row := range cases {
		o := options
		o.Native = nil
		o.Arguments.SessionID = newID()
		if row.Selected != "" {
			id := row.Selected
			o.RuntimeSelection = &providerconfig.Selection{ProviderID: &id, ModelID: "fixture-model"}
		}
		session, e := NewSession(context.Background(), o)
		if e != nil {
			t.Fatal(i, e)
		}
		environment := session.native.Environment
		actual := map[string]string{}
		for output, key := range map[string]string{"baseURL": "ANTHROPIC_BASE_URL", "token": "ANTHROPIC_AUTH_TOKEN", "apiKey": "ANTHROPIC_API_KEY", "model": "ANTHROPIC_MODEL"} {
			if value, exists := environment[key]; exists {
				actual[output] = value
			}
		}
		if !reflect.DeepEqual(actual, expected[i]) {
			t.Errorf("case%d actual nativeEnv%v Source%v", i, actual, expected[i])
		}
		if row.Selected != "" && environment["CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"] != "1" {
			t.Error("explicit provider not owned by actual host")
		}
		mu.Lock()
		before := len(seen)
		mu.Unlock()
		if e = session.Submit(context.Background(), tui.Input{Text: "verify actual selected route"}, nil); e != nil {
			t.Fatal(i, e)
		}
		if e = session.Close(context.Background()); e != nil {
			t.Fatal(i, e)
		}
		mu.Lock()
		routes := append([]string(nil), seen...)
		mu.Unlock()
		if len(routes) != before+1 || routes[before] != row.Want {
			t.Fatal("physical native route diverged", i, routes)
		}
	}
}
