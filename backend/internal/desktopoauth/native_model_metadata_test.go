package desktopoauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type noMetadataNetwork struct{ t *testing.T }

func (d noMetadataNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	d.t.Fatal("model metadata attempted network")
	return nil, nil
}
func TestMainLoopModelMetadataMatchesActualCachedCLISource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	type scenario struct {
		Env         map[string]string `json:"env"`
		Settings    map[string]any    `json:"settings"`
		Credentials map[string]any    `json:"credentials"`
		GrokTokens  any               `json:"grokTokens,omitempty"`
	}
	cases := []scenario{{}, {Env: map[string]string{"ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic"}}, {Env: map[string]string{"ANTHROPIC_MODEL": "deepseek-flash"}}, {Env: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "fake-env"}}, {Env: map[string]string{"CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_OAUTH_TOKEN": "fake-env"}}}
	for _, sub := range []any{nil, "pro", "max", "team", "enterprise"} {
		for _, tier := range []string{"default", "default_claude_max_5x"} {
			tokens := map[string]any{"accessToken": "fake-cli", "refreshToken": "fake-refresh-expired", "expiresAt": 0, "scopes": []string{"user:inference"}, "subscriptionType": sub, "rateLimitTier": tier}
			cases = append(cases, scenario{Credentials: map[string]any{"claudeAiOauth": tokens}})
		}
	}
	max := map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fake-max", "scopes": []string{"user:inference"}, "subscriptionType": "max"}}
	for _, env := range []map[string]string{{"ANTHROPIC_API_KEY": "fake-direct"}, {"ANTHROPIC_AUTH_TOKEN": "fake-bearer"}, {"CLAUDE_CODE_ENTRYPOINT": "claude-desktop", "ANTHROPIC_AUTH_TOKEN": "fake-bearer"}, {"CLAUDE_CODE_REMOTE": "1", "ANTHROPIC_API_KEY": "fake-direct"}, {"CLAUDE_CODE_DISABLE_1M_CONTEXT": "yes"}, {"CLAUDE_CODE_SIMPLE": "1"}, {"ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic"}, {"ANTHROPIC_UNIX_SOCKET": "/private-fixture/socket"}, {"ANTHROPIC_UNIX_SOCKET": "/private-fixture/socket", "CLAUDE_CODE_OAUTH_TOKEN": "fake-env"}, {"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR": "invalid"}, {"CLAUDE_CODE_USE_VERTEX": "1"}, {"ANTHROPIC_MODEL": "opus"}, {"ANTHROPIC_MODEL": "opus[1m]"}, {"ANTHROPIC_MODEL": "haiku"}, {"ANTHROPIC_DEFAULT_OPUS_MODEL": "PrivateOpus"}} {
		cases = append(cases, scenario{Env: env, Credentials: max})
	}
	cases = append(cases, scenario{Credentials: max, Settings: map[string]any{"model": "sonnet", "availableModels": []string{"sonnet"}}}, scenario{Credentials: max, Env: map[string]string{"ANTHROPIC_MODEL": "Restricted"}, Settings: map[string]any{"model": "sonnet", "availableModels": []string{"sonnet"}}}, scenario{Credentials: max, Settings: map[string]any{"apiKeyHelper": "fixture-never-run"}}, scenario{Credentials: max, Settings: map[string]any{"modelOverrides": map[string]string{"claude-opus-5-5": "OverrideOpus"}}})
	openaiToken := map[string]any{"accessToken": "fake-openai", "refreshToken": "fake-openai-refresh", "expiresAt": 0}
	cases = append(cases, scenario{Credentials: map[string]any{"openaiCodexOauth": openaiToken}}, scenario{Credentials: map[string]any{"openaiCodexOauth": openaiToken, "claudeAiOauth": map[string]any{"accessToken": "fake-console", "scopes": []string{"org:create_api_key"}}}}, scenario{Credentials: map[string]any{"openaiCodexOauth": openaiToken}, Env: map[string]string{"ANTHROPIC_API_KEY": "fake-direct"}}, scenario{Credentials: map[string]any{"openaiCodexOauth": openaiToken, "claudeAiOauth": max["claudeAiOauth"]}})
	for _, env := range []map[string]string{{"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR": "invalid"}, {"ANTHROPIC_AUTH_TOKEN": "fixture", "CLAUDE_CODE_ENTRYPOINT": "claude-desktop"}, {"CLAUDE_CODE_SIMPLE": "1"}} {
		cases = append(cases, scenario{Credentials: map[string]any{"openaiCodexOauth": openaiToken}, Env: env})
	}
	grokFile := filepath.Join(home, "private-grok.json")
	for _, model := range []string{"", "grok-4.7", "haiku", "unknown-model"} {
		for _, credentials := range []map[string]any{nil, max, {"openaiCodexOauth": openaiToken}} {
			cases = append(cases, scenario{Env: map[string]string{"GROK_OAUTH_FILE": grokFile, "CC_HAHA_GROK_OAUTH_PROVIDER": "1", "ANTHROPIC_MODEL": model}, Credentials: credentials, GrokTokens: map[string]any{"accessToken": "fake-grok", "refreshToken": "fake-grok-refresh", "expiresAt": 0}})
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-model-metadata.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_MODEL_METADATA_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cases": cases})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source metadata %v %.2200s", err, stderr.String())
	}
	var source struct {
		Rows []struct {
			Model        string
			OpenaiActive bool
		}
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(out, &source) != nil || len(source.Rows) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source escaped %v %.1200s", source.Capabilities.Blocked, out)
	}
	for i, row := range cases {
		env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config}
		for k, v := range row.Env {
			env[k] = v
		}
		if row.GrokTokens != nil {
			data, _ := json.Marshal(row.GrokTokens)
			if err := os.WriteFile(grokFile, data, 0600); err != nil {
				t.Fatal(err)
			}
		} else {
			_ = os.Remove(grokFile)
		}
		data, _ := json.Marshal(row.Credentials)
		_ = os.WriteFile(filepath.Join(config, ".credentials.json"), data, 0600)
		service, err := New(Options{Home: home, ConfigDir: config, Platform: "linux", Environment: func() map[string]string { return env }, CCROAuthTokenPath: filepath.Join(home, "absent-oauth"), CCRAPIKeyPath: filepath.Join(home, "absent-api-key"), HTTPClient: &http.Client{Transport: noMetadataNetwork{t}}})
		if err != nil {
			t.Fatal(err)
		}
		settings := protocol.NewJSONObject()
		if row.Settings != nil {
			data, _ := json.Marshal(row.Settings)
			_ = settings.UnmarshalJSON(data)
		}
		model, err := service.MainLoopModelForMetadata(context.Background(), settings, env)
		_ = service.Close()
		if err != nil || model != source.Rows[i].Model {
			t.Errorf("case%d Go %s %v Source%s", i, model, err, source.Rows[i].Model)
		}
	}
}

func TestModelMetadataReadsOriginalKeychainAndFDSlotsWithoutRefreshingOrHahaFallback(t *testing.T) {
	for _, kind := range []string{"keychain-max", "oauth-fd", "api-fd", "haha-only"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(home, ".claude")
			_ = os.MkdirAll(config, 0700)
			env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config, "USER": "fake-metadata-user"}
			descriptor := filepath.Join(home, "descriptor")
			fallback := filepath.Join(home, "absent-api-fallback")
			if kind == "oauth-fd" {
				env["CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"] = "3suffix"
				_ = os.WriteFile(descriptor, []byte("fake-fd-oauth"), 0600)
			}
			if kind == "api-fd" {
				env["CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR"] = "5"
				_ = os.WriteFile(descriptor, []byte("fake-fd-api-key"), 0600)
			}
			reads := 0
			service, err := New(Options{Home: home, ConfigDir: config, Platform: "darwin", Environment: func() map[string]string { return env }, TokenFDPath: func(int64) string { return descriptor }, APIKeyFDPath: func(int64) string { return descriptor }, CCROAuthTokenPath: filepath.Join(home, "absent-oauth"), CCRAPIKeyPath: fallback, HTTPClient: &http.Client{Transport: noMetadataNetwork{t}}, RunSecurity: func(_ context.Context, cmd securestorage.Command) ([]byte, error) {
				reads++
				if !strings.HasSuffix(cmd.Args[len(cmd.Args)-1], "-credentials-"+hexHashConfig(config)) {
					t.Fatalf("unexpected credential slot %v", cmd.Args)
				}
				if kind == "haha-only" {
					return []byte(`{}`), nil
				}
				return []byte(`{"claudeAiOauth":{"accessToken":"fake-keychain-expired","refreshToken":"fake-must-not-refresh","expiresAt":0,"scopes":["user:inference"],"subscriptionType":"max"}}`), nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			if kind == "haha-only" {
				saveFixture(t, service, []byte(`{"accessToken":"fake-haha","subscriptionType":"max"}`))
			}
			model, err := service.MainLoopModelForMetadata(context.Background(), protocol.NewJSONObject(), env)
			if err != nil {
				t.Fatal(err)
			}
			want := "claude-opus-5-5[1m]"
			if kind == "oauth-fd" {
				want = "claude-sonnet-5-5"
			}
			if kind == "api-fd" || kind == "haha-only" {
				want = "claude-opus-5-5"
			}
			if model != want {
				t.Fatalf("actual slot %s model%s want%s", kind, model, want)
			}
			if kind == "keychain-max" {
				if reads != 1 {
					t.Fatalf("secure slot reads%d", reads)
				}
				env["CLAUDE_CODE_OAUTH_TOKEN"] = "fake-changed"
				again, err := service.MainLoopModelForMetadata(context.Background(), protocol.NewJSONObject(), env)
				if err != nil || again != want {
					t.Fatalf("original CLI cache changed %s %v", again, err)
				}
			}
		})
	}
}
func hexHashConfig(config string) string {
	sum := sha256.Sum256([]byte(config))
	return hex.EncodeToString(sum[:4])
}

func TestMetadataOpenAIFileRetainsOriginalMemoizedIdentityAfterFileDeletion(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	path := filepath.Join(home, "openai.json")
	_ = os.WriteFile(path, []byte(`{"accessToken":"fake-access","refreshToken":"fake-refresh","expiresAt":0}`), 0600)
	env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config, "OPENAI_CODEX_OAUTH_FILE": path}
	service, err := New(Options{Home: home, ConfigDir: config, Platform: "linux", Environment: func() map[string]string { return env }, CCROAuthTokenPath: filepath.Join(home, "absent-oauth"), CCRAPIKeyPath: filepath.Join(home, "absent-api"), HTTPClient: &http.Client{Transport: noMetadataNetwork{t}}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	for round := 0; round < 2; round++ {
		model, err := service.MainLoopModelForMetadata(context.Background(), protocol.NewJSONObject(), env)
		if err != nil || model != "gpt-6-astra" {
			t.Fatalf("memoized OpenAI changed to Claude round%d %s %v", round, model, err)
		}
		_ = os.Remove(path)
	}
}

func TestModelMetadataOpenAIUsesSameOriginalLegacyKeySlotAndCachedOwner(t *testing.T) {
	for _, legacy := range []string{"", "private-legacy-key"} {
		t.Run(fmt.Sprintf("legacy-%t", legacy != ""), func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(home, ".claude")
			_ = os.MkdirAll(config, 0700)
			path := filepath.Join(home, "openai.json")
			_ = os.WriteFile(path, []byte(`{"accessToken":"fake-only","refreshToken":"fake-no-refresh","expiresAt":0}`), 0600)
			env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config, "USER": "private-test-user", "OPENAI_CODEX_OAUTH_FILE": path}
			reads := 0
			s, err := New(Options{Home: home, ConfigDir: config, Platform: "darwin", Environment: func() map[string]string { return env }, CCROAuthTokenPath: filepath.Join(home, "absent-oauth"), CCRAPIKeyPath: filepath.Join(home, "absent-api"), HTTPClient: &http.Client{Transport: noMetadataNetwork{t}}, RunSecurity: func(_ context.Context, command securestorage.Command) ([]byte, error) {
				if command.Args[len(command.Args)-1] == "Claude Code-credentials-"+hexHashConfig(config) {
					return []byte(`{}`), nil
				}
				reads++
				if command.Args[len(command.Args)-1] != "Claude Code-"+hexHashConfig(config) {
					t.Fatalf("wrong original legacy keychain slot %v", command.Args)
				}
				return []byte("\n" + legacy + " \n"), nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			want := "gpt-6-astra"
			if legacy != "" {
				want = "claude-opus-5-5"
			}
			for round := 0; round < 2; round++ {
				got, err := s.MainLoopModelForMetadata(context.Background(), protocol.NewJSONObject(), env)
				if err != nil || got != want {
					t.Fatalf("model %s want %s %v", got, want, err)
				}
				legacy = "changed-cached-key"
			}
			if reads != 1 {
				t.Fatal("legacy getter lost Source memoization", reads)
			}
		})
	}
}
