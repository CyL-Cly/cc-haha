package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func remoteCLIOptions(t *testing.T, base, proxy, ca string) Options {
	t.Helper()
	o := nativeSessionOptions(t, base)
	for key, value := range map[string]string{"HTTPS_PROXY": proxy, "NO_PROXY": "127.0.0.1,localhost", "NODE_EXTRA_CA_CERTS": ca, "ANTHROPIC_DEFAULT_HAIKU_MODEL": "fixture-small-model", "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1"} {
		o.Native.Environment[key] = value
		o.Environment[key] = value
	}
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("isolated Keychain unavailable")
	}
	config := o.Native.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		".credentials.json": `{"claudeAiOauth":{"accessToken":"owned-ccr-oauth","refreshToken":"owned-refresh","expiresAt":9999999999999,"subscriptionType":"max","scopes":["user:inference","user:profile"]},"futureSecure":true}`,
		".claude.json":      `{"oauthAccount":{"organizationUuid":"owned-ccr-org"},"futureGlobal":true}`,
	} {
		if err := os.WriteFile(filepath.Join(config, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return o
}

func TestPublicCLIRemoteCreatesSessionThroughCanonicalTitleAndSuppressesStartupHooks(t *testing.T) {
	var titles, creates atomic.Int32
	var created protocol.JSONObject
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		data, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/v1/messages":
			var body protocol.JSONObject
			_ = body.UnmarshalJSON(data)
			if body.String("model") != "fixture-small-model" || !strings.Contains(string(body.Get("messages")), "remote requested task") {
				t.Error("remote title bypassed canonical Source smallfast request", body)
			}
			titles.Add(1)
			response(w, "remote-title", "", nil, `{"title":"Remote canonical title","branch":"claude/remote-fixture"}`)
		case "/v1/environment_providers":
			if r.Header.Get("Authorization") != "Bearer owned-ccr-oauth" || r.Header.Get("x-organization-uuid") != "owned-ccr-org" {
				t.Error("remote API did not consume actual saved OAuth", r.Header)
			}
			_, _ = io.WriteString(w, `{"environments":[{"environment_id":"owned-cloud","kind":"anthropic_cloud"}]}`)
		case "/v1/sessions":
			if r.Method != "POST" || r.Header.Get("anthropic-beta") != "ccr-byoc-2025-07-29" {
				t.Error("remote creation contract", r.Method, r.Header)
			}
			_ = created.UnmarshalJSON(data)
			creates.Add(1)
			_, _ = io.WriteString(w, `{"id":"session_owned_cloud","title":"Remote canonical title"}`)
		default:
			t.Error("unexpected remote startup network", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer peer.Close()
	proxy, ca := claudeAIAPIPrivateTLS(t, peer.Config.Handler)
	o := remoteCLIOptions(t, peer.URL, proxy, ca)
	marker := filepath.Join(o.WorkDir, "startup-hook-must-not-run")
	settings, _ := json.Marshal(map[string]any{"hooks": map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "touch " + marker}}}}}})
	if err := os.WriteFile(filepath.Join(o.Native.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), settings, 0600); err != nil {
		t.Fatal(err)
	}
	command, _, err := ParseCommand([]string{"--remote", "remote requested task", "--model", "fixture-model", "--tools", "", "--thinking", "disabled"}, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var output bytes.Buffer
	code, err := RunCommand(ctx, command, o, strings.NewReader(""), &output, nil)
	if err != nil || code != 0 || titles.Load() != 1 || creates.Load() != 1 {
		t.Fatal(code, err, titles.Load(), creates.Load(), output.String())
	}
	if created.String("title") != "Remote canonical title" || created.String("environment_id") != "owned-cloud" || !strings.Contains(string(created.Get("events")), "remote requested task") || !strings.Contains(output.String(), "Created remote session: Remote canonical title\n") || !strings.Contains(output.String(), "Resume with: claude --teleport session_owned_cloud\n") {
		t.Fatal("public remote consumer lost Source output/session payload", created, output.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("remote API staging ran local SessionStart hooks", err)
	}
}

func TestPublicCLITeleportPrintRestoresCloudHistoryAsLocalForkAndCleansStaging(t *testing.T) {
	const sid = "session_owned_teleport"
	var calls atomic.Int32
	var actual protocol.JSONObject
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if r.URL.Path != "/v1/messages" && r.Header.Get("Authorization") != "Bearer owned-ccr-oauth" {
			t.Error("teleport API auth", r.Header)
		}
		switch r.URL.Path {
		case "/v1/sessions/" + sid:
			_, _ = io.WriteString(w, `{"id":"`+sid+`","title":"Owned cloud","session_context":{"sources":[],"outcomes":[]}}`)
		case "/v1/code/sessions/" + sid + "/teleport-events":
			_, _ = io.WriteString(w, `{"data":[{"payload":{"type":"user","uuid":"cloud-user","parentUuid":null,"sessionId":"`+sid+`","timestamp":"2026-10-01T00:00:00Z","message":{"role":"user","content":"CLOUD_OWNED_HISTORY"}}},{"payload":{"type":"assistant","uuid":"cloud-assistant","parentUuid":"cloud-user","sessionId":"`+sid+`","timestamp":"2026-10-01T00:00:01Z","message":{"role":"assistant","id":"cloud-answer","content":[{"type":"text","text":"CLOUD_OWNED_ANSWER"}]}}}],"next_cursor":null}`)
		case "/v1/messages":
			data, _ := io.ReadAll(r.Body)
			_ = actual.UnmarshalJSON(data)
			calls.Add(1)
			response(w, "teleport-local-answer", "", nil, "TELEPORT_LOCAL_FORK_READY")
		default:
			t.Error("teleport mutated cloud or called unrelated API", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer peer.Close()
	proxy, ca := claudeAIAPIPrivateTLS(t, peer.Config.Handler)
	o := remoteCLIOptions(t, peer.URL, proxy, ca)
	command, _, err := ParseCommand([]string{"--print", "--teleport", sid, "--model", "fixture-model", "--system-prompt", "teleport history fixture", "--tools", "", "--thinking", "disabled", "--", "continue cloud history"}, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var output, errorOutput bytes.Buffer
	o.Stderr = &errorOutput
	code, err := RunCommand(ctx, command, o, strings.NewReader(""), &output, nil)
	if err != nil || code != 0 || calls.Load() != 1 || !strings.Contains(output.String(), "TELEPORT_LOCAL_FORK_READY") {
		t.Fatal(code, err, calls.Load(), output.String(), errorOutput.String())
	}
	messages := string(actual.Get("messages"))
	for _, expected := range []string{"CLOUD_OWNED_HISTORY", "CLOUD_OWNED_ANSWER", "This session is being continued from another machine", "continue cloud history"} {
		if !strings.Contains(messages, expected) {
			t.Error("teleport history did not reach real model request", expected, messages)
		}
	}
	staging, err := filepath.Glob(filepath.Join(o.Native.Temp, "ccr-teleport-*.jsonl"))
	if err != nil || len(staging) != 0 {
		t.Fatal("teleport staging leaked", staging, err)
	}
	var transcripts []string
	_ = filepath.WalkDir(filepath.Join(o.Native.Environment["CLAUDE_CONFIG_DIR"], "projects"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			transcripts = append(transcripts, path)
		}
		return nil
	})
	var own bool
	for _, path := range transcripts {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), "TELEPORT_LOCAL_FORK_READY") {
			own = true
			if strings.Contains(filepath.Base(path), sid) {
				t.Error("local fork retained cloud identity", path)
			}
		}
	}
	if !own {
		t.Fatal("teleported print did not persist local fork", fmt.Sprint(transcripts))
	}
}

func TestPublicCLIPrintIgnoresRemoteLikeFrozenSource(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if r.URL.Path != "/v1/messages" {
			t.Error("print entered remote API", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		response(w, "ordinary-print", "", nil, "ORDINARY_PRINT_REMOTE_IGNORED")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	command, _, err := ParseCommand([]string{"--print", "--remote", "cloud task ignored", "--model", "fixture-model", "--system-prompt", "ordinary print", "--tools", "", "--thinking", "disabled", "--", "ordinary requested print"}, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	code, err := RunCommand(ctx, command, o, strings.NewReader(""), &output, nil)
	if err != nil || code != 0 || !strings.Contains(output.String(), "ORDINARY_PRINT_REMOTE_IGNORED") {
		t.Fatal(code, err, output.String())
	}
}

func TestPublicCLIRemoteRequiresAccountBeforeAnyModelOrCloudAPI(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		t.Error("API key entered CCR account flow", r.URL.Path)
		w.WriteHeader(500)
	}))
	defer peer.Close()
	proxy, ca := claudeAIAPIPrivateTLS(t, peer.Config.Handler)
	o := remoteCLIOptions(t, peer.URL, proxy, ca)
	if err := os.Remove(filepath.Join(o.Native.Environment["CLAUDE_CONFIG_DIR"], ".credentials.json")); err != nil {
		t.Fatal(err)
	}
	command, _, err := ParseCommand([]string{"--remote", "account-gated task", "--tools", "", "--model", "fixture-model"}, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, err := RunCommand(ctx, command, o, strings.NewReader(""), io.Discard, nil)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "API key authentication is not sufficient") {
		t.Fatal(code, err)
	}
}

func TestPublicCLIChromeSavedSubscriberInstallsNativeHostAndExitsWithoutInference(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		t.Error("Chrome startup reached unrelated model/cloud API", r.URL.Path)
		w.WriteHeader(500)
	}))
	defer peer.Close()
	proxy, ca := claudeAIAPIPrivateTLS(t, peer.Config.Handler)
	o := remoteCLIOptions(t, "https://api.anthropic.com", proxy, ca)
	delete(o.Native.Environment, "ANTHROPIC_API_KEY")
	delete(o.Environment, "ANTHROPIC_API_KEY")
	command, _, err := ParseCommand([]string{"--chrome", "--model", "fixture-model", "--tools", "", "--thinking", "disabled"}, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	code, err := RunCommand(ctx, command, o, strings.NewReader("/exit\n"), io.Discard, []string{"TERM=dumb"})
	if err != nil || code != 0 {
		t.Fatal("real Chrome startup did not settle/cancel shipped MCP stub", code, err)
	}
	config := o.Native.Environment["CLAUDE_CONFIG_DIR"]
	wrapper := filepath.Join(config, "chrome", "chrome-native-host")
	if data, err := os.ReadFile(wrapper); err != nil || !strings.Contains(string(data), "--chrome-native-host") {
		t.Fatal("public flag did not install real native host wrapper", err, string(data))
	}
	manifest := filepath.Join(o.Native.Home, "Library", "Application Support", "Google", "Chrome", "NativeMessagingHosts", "com.anthropic.claude_code_browser_extension.json")
	if data, err := os.ReadFile(manifest); err != nil || !strings.Contains(string(data), wrapper) {
		t.Fatal("public Chrome flag lost actual manifest", err, string(data))
	}
}
