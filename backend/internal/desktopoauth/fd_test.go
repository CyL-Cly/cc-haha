package desktopoauth

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

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestDescriptorCacheFallbackEmptyAndRemotePrivatePersistence(t *testing.T) {
	for _, test := range []struct {
		name, fd, input, fallback, want string
		missing, remote                 bool
	}{
		{"descriptor-prefix", "  +3trailing", " \ufefffake-from-descriptor\u00a0 ", "fake-fallback", "fake-from-descriptor", false, false},
		{"fallback-no-env", "", "", " fake-fallback ", "fake-fallback", true, false},
		{"fallback-failed-fd", "3", "", "fake-fallback", "fake-fallback", true, false},
		{"invalid-no-fallback", "bad3", "", "fake-fallback", "", true, false},
		{"empty-no-fallback", "3", " \n ", "fake-fallback", "", false, false},
		{"remote-persist", "3", "fake-descriptor", "", "fake-descriptor", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := host(t)
			s, home, env, _ := fixture(t, h)
			fdPath := filepath.Join(home, "fd-input")
			fallback := filepath.Join(home, "remote", ".oauth_token")
			s.options.TokenFDPath = func(fd int64) string {
				if fd != 3 {
					t.Errorf("parsed descriptor %d", fd)
				}
				return fdPath
			}
			env["CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"] = test.fd
			if test.remote {
				env["CLAUDE_CODE_REMOTE"] = "1"
			}
			if !test.missing {
				if err := os.WriteFile(fdPath, []byte(test.input), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if test.fallback != "" {
				_ = os.MkdirAll(filepath.Dir(fallback), 0700)
				_ = os.WriteFile(fallback, []byte(test.fallback), 0600)
			}
			token, err := s.readOAuthTokenFD(context.Background())
			if err != nil || token != test.want {
				t.Fatalf("token %q err=%v", token, err)
			}
			_ = os.WriteFile(fdPath, []byte("fake-later-token"), 0600)
			token, _ = s.readOAuthTokenFD(context.Background())
			if token != test.want {
				t.Fatal("one-shot descriptor cache changed")
			}
			if test.remote {
				raw, err := os.ReadFile(fallback)
				if err != nil || string(raw) != test.want {
					t.Fatal("remote persist")
				}
				info, _ := os.Stat(fallback)
				if info.Mode().Perm() != 0600 {
					t.Fatal("remote token mode")
				}
				info, _ = os.Stat(filepath.Dir(fallback))
				if info.Mode().Perm() != 0700 {
					t.Fatal("remote token parent mode")
				}
			}
		})
	}
}

func TestActualSourceCLIAuthFDCacheAndPriority(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	for name, inputs := range map[string]map[string]any{
		"fd-prefix":  {"fd": "  +3tail", "descriptor": " \ufefffake-fd\u00a0 ", "fallback": "fake-fallback"},
		"empty-fd":   {"fd": "3", "descriptor": " \n ", "fallback": "fake-fallback"},
		"invalid-fd": {"fd": "invalid", "fallback": "fake-fallback"},
		"fallback":   {"fallback": " fake-fallback "},
		"env":        {"envToken": "fake-env", "fd": "3", "descriptor": "fake-fd"},
		"bare":       {"bare": true, "envToken": "fake-env"},
		"plain-cli":  {"credentials": map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fake-cli", "subscriptionType": "pro", "rateLimitTier": "default"}, "unknown": "preserved"}},
	} {
		t.Run(name, func(t *testing.T) {
			h := host(t)
			s, home, env, clock := fixture(t, h)
			descriptor := filepath.Join(home, "descriptor-input")
			s.options.TokenFDPath = func(int64) string { return descriptor }
			if value, ok := inputs["descriptor"].(string); ok {
				_ = os.WriteFile(descriptor, []byte(value), 0600)
			}
			if value, ok := inputs["fallback"].(string); ok {
				_ = os.MkdirAll(filepath.Join(home, "remote"), 0700)
				_ = os.WriteFile(s.options.CCROAuthTokenPath, []byte(value), 0600)
			}
			if value, ok := inputs["fd"].(string); ok {
				env["CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"] = value
			}
			if value, ok := inputs["envToken"].(string); ok {
				env["CLAUDE_CODE_OAUTH_TOKEN"] = value
			}
			if inputs["bare"] == true {
				env["CLAUDE_CODE_SIMPLE"] = "1"
			}
			if value, ok := inputs["credentials"]; ok {
				_ = os.WriteFile(filepath.Join(s.options.ConfigDir, ".credentials.json"), encode(value), 0600)
			}
			values := []any{}
			add := func() {
				value, err := s.loadCLITokens(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if value == nil {
					values = append(values, nil)
				} else {
					values = append(values, json.RawMessage(encode(*value)))
				}
			}
			add()
			env["CLAUDE_CODE_OAUTH_TOKEN"] = "fake-changed"
			add()
			s.ClearCLICredentialCache()
			add()
			fixturePath, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-desktop-oauth.fixture.ts")
			_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + s.options.ConfigDir, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_DESKTOP_OAUTH_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
			cmd.Stdin = bytes.NewReader(encode(map[string]any{"clock": *clock, "cliAuth": inputs}))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("Source CLI auth %v %s", err, limit(stderr.String()))
			}
			var a, b any
			_ = json.Unmarshal(encode(values), &a)
			if json.Unmarshal(output, &b) != nil {
				t.Fatalf("Source JSON %s", limit(string(output)))
			}
			if !reflect.DeepEqual(a, b) {
				t.Errorf("Go=%s Source=%s", encode(values), output)
			}
		})
	}
}

func TestCLIAuthMemoPriorityBareAndNativeSecurityRead(t *testing.T) {
	h := host(t)
	s, home, env, _ := fixture(t, h)
	env["CLAUDE_CODE_OAUTH_TOKEN"] = "fake-env"
	value, err := s.loadCLITokens(context.Background())
	if err != nil || value.String("accessToken") != "fake-env" || !bytes.Equal(value.Get("scopes"), []byte(`["user:inference"]`)) {
		t.Fatal("env priority")
	}
	env["CLAUDE_CODE_OAUTH_TOKEN"] = "fake-later"
	value, _ = s.loadCLITokens(context.Background())
	if value.String("accessToken") != "fake-env" {
		t.Fatal("Source CLI memo boundary")
	}
	s.ClearCLICredentialCache()
	value, _ = s.loadCLITokens(context.Background())
	if value.String("accessToken") != "fake-later" {
		t.Fatal("cache invalidation")
	}
	s.ClearCLICredentialCache()
	env["CLAUDE_CODE_SIMPLE"] = "1"
	value, _ = s.loadCLITokens(context.Background())
	if value != nil {
		t.Fatal("bare auth read")
	}
	delete(env, "CLAUDE_CODE_SIMPLE")
	delete(env, "CLAUDE_CODE_OAUTH_TOKEN")
	s.ClearCLICredentialCache()
	// Run the real native-security selector with only its OS process seam
	// replaced. No user's actual Keychain command can run in this fixture.
	called := 0
	secure, err := securestorage.New(securestorage.Options{Home: home, ConfigDir: s.options.ConfigDir, ConfigDirExplicit: true, Platform: "darwin", Environment: env, RunSecurity: func(ctx context.Context, cmd securestorage.Command) ([]byte, error) {
		called++
		if len(cmd.Args) == 0 || cmd.Args[0] != "find-generic-password" {
			t.Errorf("native command %#v", cmd)
		}
		return []byte(`{"claudeAiOauth":{"accessToken":"fake-keychain","subscriptionType":"pro","rateLimitTier":"default"},"unknown":"preserved"}`), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	s.options.Secure = secure
	value, err = s.loadCLITokens(context.Background())
	if err != nil || value.String("accessToken") != "fake-keychain" || called != 1 {
		t.Fatalf("native security read %v %d", err, called)
	}
	value, _ = s.loadCLITokens(context.Background())
	if value.String("accessToken") != "fake-keychain" || called != 1 {
		t.Fatal("memo reread Keychain")
	}
	var decoded map[string]any
	_ = json.Unmarshal(encode(*value), &decoded)
	if len(decoded) != 3 {
		t.Fatal("CLI token shape")
	}
}
