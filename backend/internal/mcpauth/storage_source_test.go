package mcpauth

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func authFixture(t *testing.T) (*Service, string) {
	t.Helper()
	home := t.TempDir()
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude")
	for _, p := range []string{config, filepath.Join(home, "tmp")} {
		if err = os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	store, err := securestorage.New(securestorage.Options{Home: home, ConfigDir: config, Platform: "linux", Environment: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{Secure: store, Home: home, ConfigDir: config, CWD: home, Platform: runtime.GOOS, Environment: map[string]string{}, Now: func() time.Time { return time.UnixMilli(1700000000000) }})
	if err != nil {
		t.Fatal(err)
	}
	return s, home
}
func authObject(t *testing.T, text string) protocol.JSONObject {
	t.Helper()
	var result protocol.JSONObject
	if err := result.UnmarshalJSON([]byte(text)); err != nil {
		t.Fatal(err)
	}
	return result
}
func TestProviderOriginalCredentialSlotsAndRawServerIdentityAgainstActualSource(t *testing.T) {
	s, home := authFixture(t)
	config := authObject(t, `{"type":"http","url":"https://mcp.example/path?query=x","headers":{"X-Z":"last","X-A":"first"},"oauth":{"clientId":"configured-client"}}`)
	name := "plugin:office-fixture:service"
	key, err := GetServerKey(name, config)
	if err != nil {
		t.Fatal(err)
	}
	initial := record(field("future", map[string]any{"keep": true}), field("mcpOAuth", map[string]any{"unrelated|hash": map[string]any{"accessToken": "keep-other-token", "expiresAt": 1701000000000}}), field("mcpOAuthClientConfig", map[string]any{key: map[string]any{"clientSecret": "configured-secret"}}))
	if result := s.options.Secure.Update(context.Background(), initial); !result.Success {
		t.Fatal(result)
	}
	type step struct {
		Action string `json:"action"`
		Value  any    `json:"value,omitempty"`
		Scope  string `json:"scope,omitempty"`
	}
	steps := []step{{Action: "key"}, {Action: "client-info"}, {Action: "save-client", Value: map[string]any{"client_id": "registered-client", "client_secret": "registered-secret"}}, {Action: "client-info"}, {Action: "save-discovery", Value: map[string]any{"authorizationServerUrl": "https://as.example", "resourceMetadataUrl": "https://mcp.example/.well-known/oauth-protected-resource", "resourceMetadata": map[string]any{"discard": "bulky"}, "authorizationServerMetadata": map[string]any{"discard": "bulky"}}}, {Action: "discovery"}, {Action: "has-discovery"}, {Action: "save-tokens", Value: map[string]any{"access_token": "fresh-token", "refresh_token": "refresh-token", "expires_in": 600, "scope": "read write"}}, {Action: "tokens"}, {Action: "step-up", Value: "write admin"}, {Action: "tokens"}, {Action: "redirect", Value: "https://as.example/authorize?scope=write+admin"}, {Action: "invalidate", Scope: "discovery"}, {Action: "invalidate", Scope: "tokens"}, {Action: "save-secret", Value: "next-secret"}, {Action: "client-config"}, {Action: "clear-client-config"}, {Action: "invalidate", Scope: "client"}, {Action: "clear-tokens"}}
	provider, err := s.Provider(context.Background(), name, config)
	if err != nil {
		t.Fatal(err)
	}
	observations := []any{}
	for _, op := range steps {
		var value any
		var err error
		rawValue, _ := json.Marshal(op.Value)
		switch op.Action {
		case "key":
			value, err = GetServerKey(name, config)
		case "client-info":
			value, err = provider.ClientInformation(context.Background())
		case "save-client":
			err = provider.SaveClientInformation(context.Background(), object(rawValue))
		case "save-tokens":
			err = provider.SaveTokens(context.Background(), object(rawValue))
		case "tokens":
			value, err = provider.Tokens(context.Background())
		case "save-discovery":
			err = provider.SaveDiscoveryState(context.Background(), object(rawValue))
		case "discovery":
			value, err = provider.DiscoveryState(context.Background())
		case "has-discovery":
			value, err = s.HasDiscoveryWithoutToken(context.Background(), name, config)
		case "invalidate":
			err = provider.InvalidateCredentials(context.Background(), op.Scope)
		case "save-secret":
			err = s.SaveClientSecret(context.Background(), name, config, op.Value.(string))
		case "client-config":
			value, err = s.ClientConfig(context.Background(), name, config)
		case "clear-client-config":
			err = s.ClearClientConfig(context.Background(), name, config)
		case "clear-tokens":
			err = s.ClearTokens(context.Background(), name, config)
		case "step-up":
			provider.MarkStepUpPending(op.Value.(string))
		case "redirect":
			err = provider.RedirectToAuthorization(context.Background(), op.Value.(string))
		}
		if err != nil {
			t.Fatal(op.Action, err)
		}
		storage, err := s.read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		observations = append(observations, map[string]any{"value": value, "storage": storage})
	}
	compareAuthSource(t, home, map[string]any{"name": name, "config": config, "now": 1700000000000, "initial": initial, "operations": steps}, observations)
}
func compareAuthSource(t *testing.T, home string, input, actual any) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		return
	}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-mcp-auth.fixture.ts")
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Stdin = bytes.NewReader(payload)
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_NATIVE_MCP_AUTH_FIXTURE=1", "NODE_ENV=production", "USER=owned-user", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	expected, err := command.Output()
	if err != nil {
		t.Fatalf("actual Source MCP OAuth: %v %s", err, stderr.Bytes())
	}
	actualJSON, _ := json.Marshal(actual)
	var left, right any
	if json.Unmarshal(actualJSON, &left) != nil || json.Unmarshal(expected, &right) != nil {
		t.Fatalf("invalid auth observations %s", expected)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("MCP OAuth full storage observations differ\nGo %s\nSource %s", actualJSON, expected)
	}
}
