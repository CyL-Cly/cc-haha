package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOrdinaryMainCommandProviderEnvironmentAgainstActualSource(t *testing.T) {
	for _, mode := range []string{"anthropic", "openai", "openai-chat", "openai-responses", "nested-proxy", "host-managed", "host-zero", "official", "missing-active", "empty-index", "no-index", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			root, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			home := filepath.Join(root, "home")
			config := filepath.Join(home, ".claude")
			dir := filepath.Join(config, "cc-haha")
			if e = os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			write := func(name, body string) {
				if e = os.WriteFile(name, []byte(body), 0600); e != nil {
					t.Fatal(e)
				}
			}
			write(filepath.Join(config, "settings.json"), `{"env":{"OWNED_TRUSTED":"actual-user-env"},"future":{"keep":true}}`)
			write(filepath.Join(dir, "settings.json"), `{"env":{"ANTHROPIC_AUTH_TOKEN":"legacy-stale","ANTHROPIC_DEFAULT_OPUS_MODEL":"legacy-stale","OWNED_LEGACY":"legacy-owned"},"future":true}`)
			format := "anthropic"
			nested := true
			if mode == "openai" {
				format = "openai"
			}
			if mode == "openai-chat" || mode == "host-zero" {
				format = "openai_chat"
			}
			if mode == "openai-responses" {
				format = "openai_responses"
			}
			if mode == "nested-proxy" {
				nested = false
			}
			index := map[string]any{"activeId": "owned-provider", "providers": []any{map[string]any{"id": "owned-provider", "name": "Owned", "presetId": "", "baseUrl": "http://127.0.0.1:1", "apiKey": "fake-provider-only", "apiFormat": format, "authStrategy": "auth_token", "supportsNestedToolResultMedia": nested, "models": map[string]string{"main": "owned-main", "haiku": "owned-haiku", "sonnet": "owned-sonnet", "opus": "owned-opus"}, "future": "keep"}}, "futureIndex": true}
			if mode == "official" {
				index["activeId"] = "openai-official"
			}
			if mode == "missing-active" {
				index["activeId"] = "missing-owned"
			}
			encoded, _ := json.Marshal(index)
			if mode == "empty-index" {
				encoded = []byte(`{}`)
			}
			if mode == "malformed" {
				encoded = []byte(`{"broken":`)
			}
			if mode != "no-index" {
				write(filepath.Join(dir, "providers.json"), string(encoded))
			}
			env := map[string]string{"HOME": home, "USERPROFILE": home, "USER": "owned", "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "PATH": "/usr/bin:/bin:/opt/homebrew/bin", "USER_TYPE": "external", "NODE_ENV": "production", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1", "CC_HAHA_CLI_PROVIDER_FIXTURE": "1", "CC_HAHA_FIXTURE_TEMP_ROOT": root, "BUN_OPTIONS": "--no-env-file"}
			if mode == "host-managed" {
				env["CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"] = "1"
				env["ANTHROPIC_BASE_URL"] = "http://127.0.0.1:2"
				env["ANTHROPIC_AUTH_TOKEN"] = "fake-host-only"
			}
			if mode == "host-zero" {
				env["CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"] = "0"
			}
			sourceEnv := []string{}
			for key, value := range env {
				sourceEnv = append(sourceEnv, key+"="+value)
			}
			fixture, _ := filepath.Abs("../../testdata/native-cli-provider-environment.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
			child.Dir = home
			child.Env = sourceEnv
			child.Stdin = strings.NewReader(`{"homeDir":` + string(quoteMain(home)) + `}`)
			var diagnostics bytes.Buffer
			child.Stderr = &diagnostics
			raw, e := child.Output()
			if e != nil {
				t.Fatal("actual Source environment", e, diagnostics.String())
			}
			var expected struct {
				Environment map[string]string `json:"environment"`
				Health      any               `json:"health"`
			}
			if e = json.Unmarshal(raw, &expected); e != nil {
				t.Fatal(e, string(raw))
			}
			native, e := NewProcessNativeBoundary(NativeProcessOptions{Environment: env, CWD: home, Platform: "linux", ManagedRoot: filepath.Join(root, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			if e != nil {
				t.Fatal(e)
			}
			native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
				t.Error("real credential primitive reached")
				return nil, os.ErrPermission
			}
			command, e := NewMainCommand(MainCommandOptions{Arguments: []string{"plugin", "marketplace", "list", "--json"}, Native: native, Platform: "linux", PID: os.Getpid(), Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
			if e != nil {
				t.Fatal("actual ordinary CLI startup", e)
			}
			defer command.Close()
			actual := map[string]string{}
			keys := append(providerconfig.ManagedKeys(), "OWNED_LEGACY", "OWNED_TRUSTED")
			for _, key := range keys {
				if value, present := native.Environment[key]; present {
					actual[key] = value
				}
			}
			var health any
			endpoint := actual["ANTHROPIC_BASE_URL"]
			if expected.Health != nil {
				address, e := url.Parse(endpoint)
				if e != nil || address.Hostname() != "127.0.0.1" || address.Path != "/proxy" || address.Port() == "" {
					t.Fatal("actual CLI proxy endpoint", endpoint, e)
				}
				response, e := http.Get(address.Scheme + "://" + address.Host + "/health")
				if e != nil {
					t.Fatal(e)
				}
				payload, _ := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if e = json.Unmarshal(payload, &health); e != nil {
					t.Fatal(e)
				}
				actual["ANTHROPIC_BASE_URL"] = "http://127.0.0.1:<PORT>/proxy"
			}
			if !reflect.DeepEqual(expected.Environment, actual) || !reflect.DeepEqual(expected.Health, health) {
				t.Fatalf("Source env%v health%v; Go env%v health%v", expected.Environment, expected.Health, actual, health)
			}
			command.Close()
			if expected.Health != nil {
				address, _ := url.Parse(endpoint)
				client := http.Client{Timeout: time.Second}
				if response, e := client.Get(address.Scheme + "://" + address.Host + "/health"); e == nil {
					_ = response.Body.Close()
					t.Fatal("ordinary CLI proxy listener survived Close")
				}
			}
			after, e := os.ReadFile(filepath.Join(dir, "providers.json"))
			if mode == "no-index" {
				if !os.IsNotExist(e) {
					t.Fatal("missing CLI index created", e)
				}
			} else if e != nil || !bytes.Equal(after, encoded) {
				t.Fatal("provider index changed", e)
			}
		})
	}
}

func TestOrdinaryCommandProviderProxyUsesOwnedTLSAndClosesPendingRequest(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	config := filepath.Join(home, ".claude")
	if err = os.MkdirAll(filepath.Join(config, "cc-haha"), 0700); err != nil {
		t.Fatal(err)
	}
	started, closed := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer fake-command-tls" {
			t.Error("actual saved provider transport", r.URL.Path, r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"model":"owned-chat"`)) || !bytes.Contains(body, []byte("private pending transport")) {
			t.Error("actual converted message absent", string(body))
		}
		if requests.Add(1) <= 2 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"owned-response","object":"chat.completion","model":"owned-chat","choices":[{"index":0,"message":{"role":"assistant","content":"owned external transport"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
			return
		}
		close(started)
		<-r.Context().Done()
		close(closed)
	}))
	defer peer.Close()
	ca := filepath.Join(home, "owned-ca.pem")
	if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	index := map[string]any{"activeId": "owned-provider", "providers": []any{map[string]any{"id": "owned-provider", "name": "Owned", "presetId": "", "baseUrl": peer.URL, "apiKey": "fake-command-tls", "apiFormat": "openai_chat", "models": map[string]string{"main": "owned-chat", "haiku": "owned-chat", "sonnet": "owned-chat", "opus": "owned-chat"}, "future": true}}, "futureIndex": true}
	raw, _ := json.Marshal(index)
	if err = os.WriteFile(filepath.Join(config, "cc-haha/providers.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"network":{"proxy":{"mode":"system"},"aiRequestTimeoutMs":60000},"futureNetwork":{"keep":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "USER": "owned", "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "PATH": "/usr/bin:/bin", "NODE_EXTRA_CA_CERTS": ca, "USER_TYPE": "external", "NODE_ENV": "production", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1"}

	sourceEnv := []string{"CC_HAHA_CLI_PROVIDER_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "BUN_OPTIONS=--no-env-file"}
	for key, value := range env {
		sourceEnv = append(sourceEnv, key+"="+value)
	}
	probeBody := json.RawMessage(`{"model":"owned-chat","max_tokens":64,"messages":[{"role":"user","content":"private pending transport"}],"stream":false}`)
	input, _ := json.Marshal(map[string]any{"homeDir": home, "probe": probeBody})
	fixture, _ := filepath.Abs("../../testdata/native-cli-provider-environment.fixture.ts")
	sourceCtx, sourceCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer sourceCancel()
	source := exec.CommandContext(sourceCtx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
	source.Dir = home
	source.Env = sourceEnv
	source.Stdin = bytes.NewReader(input)
	var sourceErrors bytes.Buffer
	source.Stderr = &sourceErrors
	sourceResult, err := source.Output()
	if err != nil {
		t.Fatal("original actual TLS proxy", err, sourceErrors.String())
	}
	var oracle struct {
		Probe struct {
			Status int `json:"status"`
			Body   any `json:"body"`
		} `json:"probe"`
	}
	if err = json.Unmarshal(sourceResult, &oracle); err != nil || oracle.Probe.Status != 200 {
		t.Fatal("actual Source TLS response", err, string(sourceResult))
	}
	sourceIndex, err := os.ReadFile(filepath.Join(config, "cc-haha/providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	native, err := NewProcessNativeBoundary(NativeProcessOptions{Environment: env, CWD: home, Platform: "linux", ManagedRoot: filepath.Join(root, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		t.Error("real credential store reached")
		return nil, os.ErrPermission
	}
	command, err := NewMainCommand(MainCommandOptions{Arguments: []string{"plugin", "marketplace", "list", "--json"}, Native: native, Platform: "linux", PID: os.Getpid(), Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	defer command.Close()
	endpoint := native.Environment["ANTHROPIC_BASE_URL"]

	response, err := http.Post(endpoint+"/v1/messages", "application/json", bytes.NewReader(probeBody))
	if err != nil {
		t.Fatal(err)
	}
	responseData, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var goBody any
	if err = json.Unmarshal(responseData, &goBody); err != nil {
		t.Fatal(err, string(responseData))
	}
	if response.StatusCode != oracle.Probe.Status || !reflect.DeepEqual(goBody, oracle.Probe.Body) {
		t.Fatal("actual Source/Go TLS proxy differs", oracle.Probe.Body, goBody)
	}
	result := make(chan struct{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		request, _ := http.NewRequestWithContext(ctx, "POST", endpoint+"/v1/messages", strings.NewReader(`{"model":"owned-chat","max_tokens":64,"messages":[{"role":"user","content":"private pending transport"}],"stream":false}`))
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		result <- struct{}{}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("actual Owner-bound proxy failed to reach TLS upstream")
	}
	joined := make(chan struct{})
	go func() { command.Close(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("command Close did not join held request")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("upstream TLS socket survived command Close")
	}
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("proxy client survived command Close")
	}
	if after, err := os.ReadFile(filepath.Join(config, "cc-haha/providers.json")); err != nil || !bytes.Equal(after, sourceIndex) {
		t.Fatal("provider file differs from actual Source post-request state", err, string(sourceIndex), string(after))
	}
}
