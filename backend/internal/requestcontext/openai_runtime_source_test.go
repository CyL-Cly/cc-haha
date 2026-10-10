package requestcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

func TestOpenAIFirstPartyOriginalRequestAgainstActualQueryModelConsumer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	input := basicContext(t)
	input.Host.Profile = NativeDirectRelayProfile
	input.Host.Env = map[string]string{}
	input.Host.DeviceID, input.Host.SessionID = "private-device", "00000000-0000-4000-8000-000000000000"
	input.Options.Model = "gpt-6-sol"
	max := 64.0
	input.Options.MaxOutputTokensOverride = &max
	tokens := protocol.NewJSONObject(protocol.JSONField{Name: "accessToken", Value: []byte(`"private-access"`)}, protocol.JSONField{Name: "refreshToken", Value: []byte(`"private-refresh"`)}, protocol.JSONField{Name: "expiresAt", Value: []byte(`1000000000000000`)})
	input.Host.NativeCodexOAuth = &provider.OpenAICodexConfig{HasAuth: func(context.Context) (bool, error) { t.Error("construction resolved credentials"); return false, nil }, FreshTokens: func(context.Context) (protocol.JSONObject, error) {
		t.Error("construction refreshed credentials")
		return tokens, nil
	}}
	goRequest, err := Build(input, dependencies())
	if err != nil {
		t.Fatal(err)
	}
	var original protocol.JSONObject
	if err = original.UnmarshalJSON(goRequest.PreparedRequest.Body); err != nil {
		t.Fatal(err)
	}
	mapped, err := providerproxy.PrepareOpenAIRequest(original, tokens, map[string]string{}, []byte(`"`+input.Host.SessionID+`"`), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-openai-runtime-request.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_OPENAI_RUNTIME_REQUEST_FIXTURE=1", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "CLAUDE_CODE_MAX_RETRIES=0", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	proxy := "http://127.0.0.1:43210"
	raw, _ := json.Marshal(map[string]any{"home": home, "tokens": tokens, "history": input.History, "system": input.SystemPrompt, "device": input.Host.DeviceID, "session": input.Host.SessionID, "model": input.Options.Model, "maxTokens": max, "proxy": proxy, "noProxy": "*"})
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual original runtime %v %s", err, stderr.String())
	}
	var want struct {
		OriginalBody protocol.JSONObject `json:"originalBody"`
		Upstream     struct {
			Endpoint string              `json:"endpoint"`
			Body     protocol.JSONObject `json:"body"`
			Headers  protocol.JSONObject `json:"headers"`
			Proxy    string              `json:"proxy"`
		} `json:"upstream"`
		Phases []string `json:"phases"`
	}
	if err = json.Unmarshal(out, &want); err != nil {
		t.Fatal(err, string(out))
	}
	compare := func(label string, a, b any) {
		t.Helper()
		aa, _ := json.Marshal(a)
		bb, _ := json.Marshal(b)
		var x, y any
		_ = json.Unmarshal(aa, &x)
		_ = json.Unmarshal(bb, &y)
		if !reflect.DeepEqual(x, y) {
			t.Errorf("%s actual %s want %s", label, aa, bb)
		}
	}
	compare("original SDK params", original, want.OriginalBody)
	compare("actual remapped body", mapped.Body, want.Upstream.Body)
	compare("actual remapped headers", mapped.Headers, want.Upstream.Headers)
	if want.Upstream.Endpoint != mapped.Endpoint {
		t.Error("actual endpoint", want.Upstream.Endpoint)
	}
	exchange, err := provider.NativeDirectHTTPExchangeFromEnvironment("", map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "*"})
	if err != nil || want.Upstream.Proxy != proxy || exchange.ProxyURL != want.Upstream.Proxy {
		t.Errorf("actual original SDK proxy %q mapped transport %+v %v", want.Upstream.Proxy, exchange, err)
	}
	if !reflect.DeepEqual(want.Phases, []string{"cached_auth", "sdk_fetch", "fresh_tokens", "upstream_fetch", "cached_auth"}) {
		t.Errorf("original phases %v", want.Phases)
	}
}
