package plugincli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/plugincli"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOriginalCLIMarketplacePhysicalCommands(t *testing.T) {
	for _, action := range []string{"add", "add-existing", "list", "list-json", "remove", "update", "update-all", "update-missing", "add-invalid", "add-sparse", "add-scope", "add-url", "update-url", "add-git", "update-git", "add-policy", "remove-seed", "update-seed", "update-all-seed", "add-seed-conflict", "add-collision", "add-file", "remove-unknown", "update-local-missing", "add-invalid-schema", "add-url404", "update-url404", "add-schema-error", "add-syntax-error"} {
		t.Run(action, func(t *testing.T) { pairedMarketplace(t, action) })
	}
}
func pairedMarketplace(t *testing.T, action string) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var peer *httptest.Server
	var requestsMu sync.Mutex
	requests := []http.Header{}
	certificate := ""
	if strings.Contains(action, "url") || action == "add-git" || action == "update-git" {
		var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestsMu.Lock()
			requests = append(requests, r.Header.Clone())
			requestsMu.Unlock()
			if strings.HasSuffix(action, "404") {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"fixture","owner":{"name":"Fixture"},"plugins":[]}`))
		})
		if strings.Contains(action, "git") {
			repo := filepath.Join(root, "owned-repo")
			_ = os.MkdirAll(filepath.Join(repo, ".claude-plugin"), 0700)
			if err = os.WriteFile(filepath.Join(repo, ".claude-plugin", "marketplace.json"), []byte(`{"name":"fixture","owner":{"name":"Fixture"},"plugins":[]}`), 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(repo, "physical.txt"), []byte("actual Git remote fixture\n"), 0600); err != nil {
				t.Fatal(err)
			}
			git := func(args ...string) {
				cmd := exec.Command("/usr/bin/git", args...)
				cmd.Env = []string{"HOME=" + root, "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1"}
				if data, err := cmd.CombinedOutput(); err != nil {
					t.Fatal(err, string(data))
				}
			}
			git("init", "-q", repo)
			git("-C", repo, "add", ".")
			git("-C", repo, "-c", "user.name=OwnedFixture", "-c", "user.email=owned@fixture.invalid", "commit", "-qm", "fixture")
			bareRoot := filepath.Join(root, "owned-git-server")
			_ = os.MkdirAll(bareRoot, 0700)
			git("clone", "--bare", "-q", repo, filepath.Join(bareRoot, "fixture.git"))
			handler = &cgi.Handler{Path: "/usr/bin/git", Args: []string{"http-backend"}, Dir: root, Env: []string{"HOME=" + root, "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_PROJECT_ROOT=" + bareRoot, "GIT_HTTP_EXPORT_ALL=1"}}
		}
		peer = httptest.NewTLSServer(handler)
		defer peer.Close()
		certificate = filepath.Join(root, "owned-ca.pem")
		if err = os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600); err != nil {
			t.Fatal(err)
		}
	}
	setup := func(name string) (string, map[string]string) {
		home := filepath.Join(root, name)
		config := filepath.Join(home, ".claude")
		market := filepath.Join(home, "market")
		for _, dir := range []string{config, filepath.Join(config, "plugins"), filepath.Join(market, ".claude-plugin")} {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
		}
		write := func(path, body string) {
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
		}
		write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":"fixture","owner":{"name":"Fixture"},"plugins":[]}`)
		write(filepath.Join(config, "settings.json"), `{"future":{"keep":true},"extraKnownMarketplaces":{"fixture":{"source":{"source":"directory","path":"`+market+`"}}},"enabledPlugins":{"alpha@fixture":true,"other@elsewhere":false}}`)
		known := map[string]any{}
		if !strings.HasPrefix(action, "add") || action == "add-existing" || action == "add-seed-conflict" || action == "add-collision" {
			known["fixture"] = map[string]any{"source": map[string]string{"source": "directory", "path": market}, "installLocation": market, "lastUpdated": "2024-01-01T00:00:00Z"}
		}
		if strings.HasPrefix(action, "update-url") {
			cache := filepath.Join(config, "plugins", "marketplaces")
			_ = os.MkdirAll(cache, 0700)
			location := filepath.Join(cache, "fixture")
			write(location, `{"name":"fixture","owner":{"name":"Fixture"},"plugins":[]}`)
			known["fixture"] = map[string]any{"source": map[string]string{"source": "url", "url": peer.URL + "/marketplace.json"}, "installLocation": location, "lastUpdated": "2024-01-01T00:00:00Z"}
		}
		if action == "add-collision" {
			old := filepath.Join(home, "old-local-market")
			_ = os.MkdirAll(old, 0700)
			write(filepath.Join(old, "keep.txt"), "local collision source must survive\n")
			known["fixture"] = map[string]any{"source": map[string]string{"source": "directory", "path": old}, "installLocation": old, "lastUpdated": "2024-01-01T00:00:00Z"}
		}
		if strings.Contains(action, "seed") {
			seed := filepath.Join(home, "owned-seed")
			_ = os.MkdirAll(seed, 0700)
			known["fixture"] = map[string]any{"source": map[string]string{"source": "directory", "path": seed}, "installLocation": seed, "lastUpdated": "2024-01-01T00:00:00Z"}
		}
		if action == "add-schema-error" || action == "update-schema-error" {
			write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"owner":{"name":"Fixture"},"plugins":[]}`)
		}
		if action == "add-syntax-error" || action == "update-syntax-error" {
			write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":}`)
		}
		if action == "add-invalid-schema" {
			write(filepath.Join(market, ".claude-plugin", "marketplace.json"), `{"name":"fixture","owner":{"name":"Fixture"},"plugins":[],"typo":true}`)
		}
		if action == "update-local-missing" {
			if err = os.Remove(filepath.Join(market, ".claude-plugin", "marketplace.json")); err != nil {
				t.Fatal(err)
			}
		}
		raw, _ := json.Marshal(known)
		write(filepath.Join(config, "plugins", "known_marketplaces.json"), string(raw))
		env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "USER": "fixture", "PATH": "/usr/bin:/bin:/opt/homebrew/bin", "BUN_OPTIONS": "--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT": root, "CC_HAHA_PLUGIN_CLI_FIXTURE": "1", "NODE_ENV": "production", "USER_TYPE": "external", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1"}
		if action == "add-policy" {
			env["CC_HAHA_PLUGIN_CLI_POLICY"] = `{"strictKnownMarketplaces":[]}`
		}
		if strings.Contains(action, "seed") {
			env["CLAUDE_CODE_PLUGIN_SEED_DIR"] = filepath.Join(home, "owned-seed")
		}
		if peer != nil {
			env["NODE_EXTRA_CA_CERTS"] = certificate
			env["GIT_SSL_CAINFO"] = certificate
		}
		if action == "update-git" {
			location := filepath.Join(config, "plugins", "marketplaces", "fixture")
			_ = os.MkdirAll(filepath.Dir(location), 0700)
			cmd := exec.Command("/usr/bin/git", "clone", "-q", peer.URL+"/fixture.git", location)
			for k, v := range env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
			if data, err := cmd.CombinedOutput(); err != nil {
				t.Fatal(err, string(data))
			}
			known["fixture"] = map[string]any{"source": map[string]string{"source": "git", "url": peer.URL + "/fixture.git"}, "installLocation": location, "lastUpdated": "2024-01-01T00:00:00Z"}
			raw, _ := json.Marshal(known)
			write(filepath.Join(config, "plugins", "known_marketplaces.json"), string(raw))
		}
		return home, env
	}
	sourceHome, sourceEnv := setup("source-home")
	goHome, goEnv := setup("go-home")
	command := plugincli.Command{}
	switch action {
	case "add", "add-existing":
		command.Action = "marketplace-add"
		command.Argument = filepath.Join(goHome, "market")
	case "add-git":
		command.Action = "marketplace-add"
		command.Argument = peer.URL + "/fixture.git"
	case "update-git":
		command.Action = "marketplace-update"
		command.Argument = "fixture"
	case "add-url", "add-url404":
		command.Action = "marketplace-add"
		command.Argument = peer.URL + "/marketplace.json"
	case "update-url", "update-url404":
		command.Action = "marketplace-update"
		command.Argument = "fixture"
	case "add-policy", "add-seed-conflict", "add-collision", "add-invalid-schema", "add-schema-error", "add-syntax-error":
		command.Action = "marketplace-add"
		command.Argument = filepath.Join(goHome, "market")
	case "add-file":
		command.Action = "marketplace-add"
		command.Argument = filepath.Join(goHome, "market/.claude-plugin/marketplace.json")
	case "remove-seed", "remove-unknown":
		command.Action = "marketplace-remove"
		command.Argument = "fixture"
		if action == "remove-unknown" {
			command.Argument = "missing"
		}
	case "update-seed", "update-all-seed", "update-local-missing", "update-schema-error", "update-syntax-error":
		command.Action = "marketplace-update"
		if action != "update-all-seed" {
			command.Argument = "fixture"
		}
	case "add-invalid":
		command.Action = "marketplace-add"
		command.Argument = "invalid"
	case "add-sparse":
		command.Action = "marketplace-add"
		command.Argument = filepath.Join(goHome, "market")
		command.Sparse = []string{"plugins"}
	case "add-scope":
		command.Action = "marketplace-add"
		command.Argument = filepath.Join(goHome, "market")
		command.Scope = "managed"
	case "list", "list-json":
		command.Action = "marketplace-list"
		command.JSON = action == "list-json"
	case "remove":
		command.Action = "marketplace-remove"
		command.Argument = "fixture"
	case "update", "update-all", "update-missing":
		command.Action = "marketplace-update"
		if action != "update-all" {
			command.Argument = "fixture"
		}
		if action == "update-missing" {
			command.Argument = "missing"
		}
	}
	sourceAction := map[string]string{"marketplace-add": "marketplaceAdd", "marketplace-list": "marketplaceList", "marketplace-remove": "marketplaceRemove", "marketplace-update": "marketplaceUpdate"}[command.Action]
	argument := strings.ReplaceAll(command.Argument, goHome, sourceHome)
	payload, _ := json.Marshal(map[string]any{"action": sourceAction, "argument": argument, "options": map[string]any{"json": command.JSON, "scope": command.Scope, "sparse": command.Sparse}})
	// An absent scope is undefined in the original handler, rather than an empty explicit string.
	var input map[string]any
	_ = json.Unmarshal(payload, &input)
	if command.Scope == "" {
		delete(input["options"].(map[string]any), "scope")
	}
	payload, _ = json.Marshal(input)
	sourceEnv["CC_HAHA_PLUGIN_CLI_INPUT"] = string(payload)
	fixture, _ := filepath.Abs("../../testdata/native-cli-plugins.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	process.Dir = sourceHome
	for k, v := range sourceEnv {
		process.Env = append(process.Env, k+"="+v)
	}
	var sourceOut, sourceErr bytes.Buffer
	process.Stdout = &sourceOut
	process.Stderr = &sourceErr
	code := 0
	if err := process.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: goEnv, CWD: goHome, Platform: "darwin", ManagedRoot: filepath.Join(goHome, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) {
		if policy := goEnv["CC_HAHA_PLUGIN_CLI_POLICY"]; policy != "" {
			var value protocol.JSONObject
			_ = value.UnmarshalJSON([]byte(policy))
			return &value, nil, nil
		}
		return nil, nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
	main, err := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Arguments: []string{"plugin", "marketplace", "list"}, Native: native, Platform: "darwin", Version: "999.0.0-local", PID: os.Getpid(), Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	defer main.Close()
	var got plugincli.Result
	err = main.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		got, err = main.PluginCommand(ctx, scope, command)
		return err
	})
	if err != nil {
		t.Fatal("Go actual command", err)
	}
	normalize := func(s, home string) string { return strings.ReplaceAll(s, home, "<HOME>") }
	if code != got.ExitCode || normalize(sourceOut.String(), sourceHome) != normalize(got.Stdout, goHome) || normalize(sourceErr.String(), sourceHome) != normalize(got.Stderr, goHome) {
		t.Fatalf("Source exit=%d stdout=%q stderr=%q Go=%+v", code, sourceOut.String(), sourceErr.String(), got)
	}
	for _, rel := range []string{".claude/settings.json", ".claude/plugins/known_marketplaces.json"} {
		read := func(home string) any {
			data, err := os.ReadFile(filepath.Join(home, rel))
			if err != nil {
				t.Fatal(err)
			}
			var v any
			if err = json.Unmarshal([]byte(normalize(string(data), home)), &v); err != nil {
				t.Fatal(err)
			}
			if rows, ok := v.(map[string]any); ok && strings.Contains(rel, "known_") {
				for _, row := range rows {
					if object, ok := row.(map[string]any); ok {
						delete(object, "lastUpdated")
					}
				}
			}
			return v
		}
		if a, b := read(sourceHome), read(goHome); !reflect.DeepEqual(a, b) {
			t.Fatalf("physical %s Source=%v Go=%v", rel, a, b)
		}
	}
	if peer != nil && !strings.Contains(action, "git") {
		requestsMu.Lock()
		captured := append([]http.Header{}, requests...)
		requestsMu.Unlock()
		if len(captured) != 2 {
			t.Fatalf("expected actual Source and Go TLS requests, got %d", len(captured))
		}
		for _, key := range []string{"User-Agent", "Accept", "Accept-Encoding"} {
			if captured[0].Get(key) != captured[1].Get(key) {
				t.Fatalf("actual Source/Go TLS %s mismatch Source=%q Go=%q", key, captured[0].Get(key), captured[1].Get(key))
			}
		}
		if !strings.HasSuffix(action, "404") {
			a, err := os.ReadFile(filepath.Join(sourceHome, ".claude", "plugins", "marketplaces", "fixture"))
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(goHome, ".claude", "plugins", "marketplaces", "fixture"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(a), protocol.StringifyValidatedJavaScriptJSON(b)) {
				t.Fatalf("actual cached TLS Source %s Go %s", a, b)
			}
		}
	}
	if strings.Contains(action, "git") {
		for _, rel := range []string{".claude-plugin/marketplace.json", "physical.txt"} {
			a, err := os.ReadFile(filepath.Join(sourceHome, ".claude/plugins/marketplaces/fixture", rel))
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(goHome, ".claude/plugins/marketplaces/fixture", rel))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(a, b) {
				t.Fatalf("actual Git materialized %s differs", rel)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(goHome, "market", ".claude-plugin", "marketplace.json")); err != nil && action != "update-local-missing" {
		t.Fatal("local source was removed", err)
	}
}
