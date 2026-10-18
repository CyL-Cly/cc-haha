package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualNativeClientCredentialStatKeepsSameMtimeAndReplacesNextBearerLikeSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("Pinned actual Source required")
	}
	token := func(value string, refresh any, expiry any) map[string]any {
		return map[string]any{"accessToken": value, "refreshToken": refresh, "expiresAt": expiry, "scopes": []string{"user:inference", "user:profile"}, "subscriptionType": "max"}
	}
	steps := []map[string]any{
		{"name": "FIRST_LOCAL_TOKEN", "mtime": 2000, "token": token("fake-local-A", nil, 9999999999999)},
		{"name": "SAME_MTIME_CACHE", "mtime": 2000, "token": token("fake-local-B", nil, 9999999999999)},
		{"name": "CHANGED_MTIME_TOKEN", "mtime": 2001, "token": token("fake-local-B", nil, 9999999999999)},
		{"name": "FUTURE_REFRESHABLE", "mtime": 2002, "token": token("fake-local-C", "fake-never-refresh", 9999999999999)},
		{"name": "EXPIRED_NO_REFRESH", "mtime": 2003, "token": token("fake-local-D", nil, 0)},
		{"name": "REMOVED_PRIVATE_CREDENTIALS", "mtime": 2004, "token": nil},
	}
	var mu sync.Mutex
	headers := [][2]string{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		headers = append(headers, [2]string{r.Header.Get("Authorization"), r.Header.Get("x-api-key")})
		mu.Unlock()
		if string(objMain(body).Get("stream")) != "true" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"private-stat","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"PRIVATE_LOCAL_STAT_OK"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`)
		} else {
			emitPlanFixture(w, 1, "", "PRIVATE_LOCAL_STAT_OK")
		}
	}))
	defer peer.Close()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{"primaryApiKey":"fake-managed-fallback"}`), 0600); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-claude-local-stat.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir, cmd.Env = home, []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "USER_TYPE=external", "ANTHROPIC_BASE_URL=" + peer.URL, "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLAUDE_LOCAL_STAT_FIXTURE=1"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "peer": peer.URL, "steps": steps}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct {
		Outcomes []string
		Blocked  []string
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Outcomes) != len(steps) || len(source.Blocked) != 0 {
		t.Fatal(err, string(output))
	}
	mu.Lock()
	expected := append([][2]string(nil), headers...)
	headers = nil
	mu.Unlock()
	if !reflect.DeepEqual(expected, [][2]string{{"Bearer fake-local-A", ""}, {"Bearer fake-local-A", ""}, {"Bearer fake-local-B", ""}, {"Bearer fake-local-C", ""}, {"Bearer fake-local-D", ""}, {"", "fake-managed-fallback"}}) {
		t.Fatal("Actual Source header contract", expected)
	}
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.write(t, filepath.Join(f.config, ".claude.json"), `{"primaryApiKey":"fake-managed-fallback"}`)
	f.n.Environment["ANTHROPIC_BASE_URL"], f.n.Environment["CLAUDE_CODE_MAX_RETRIES"], f.n.Environment["USER_TYPE"] = peer.URL, "0", "external"
	path := filepath.Join(f.config, ".credentials.json")
	f.write(t, path, string(rawMain(map[string]any{"claudeAiOauth": steps[0]["token"]})))
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "claude-sonnet-4-5", Tools: []string{""}, MaxTurns: 1, SystemPrompt: "private local credential test", HasCustomSystemPrompt: true, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnOutput: func([]byte) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := m.ShutdownSession(context.Background()); e != nil {
			t.Error(e)
		}
	}()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	for i, step := range steps {
		if step["token"] == nil {
			err = os.Remove(path)
		} else {
			err = os.WriteFile(path, rawMain(map[string]any{"claudeAiOauth": step["token"]}), 0600)
			if err == nil {
				stamp := time.Unix(int64(step["mtime"].(int)), 0)
				err = os.Chtimes(path, stamp, stamp)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain(time.Unix(int64(i), 0).String())}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": step["name"]})})
		if err = m.RunTurn(ctx, input); err != nil {
			t.Fatal(step["name"], err)
		}
	}
	mu.Lock()
	actual := append([][2]string(nil), headers...)
	mu.Unlock()
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("actual Source vs same NativeMainPrint next HTTP", actual, expected)
	}
}

func TestNativeClaudeLocalStatExpiredRefreshableAndForeignRemainExplicit(t *testing.T) {
	m := openAIAuthMainFixture(t)
	path := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], ".credentials.json")
	if err := os.WriteFile(path, []byte(`{"claudeAiOauth":{"accessToken":"fake-expired","refreshToken":"fake-do-not-refresh","expiresAt":1,"scopes":["user:inference"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		err := m.openAIAuth.checkClaudeTokensBeforeClient(context.Background(), scope)
		var capability *MainCapabilityError
		if !errors.As(err, &capability) || capability.Boundary != "Claude OAuth expired refreshable token producer" {
			return errors.New("expired refreshable silently entered no-refresh profile")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	other := cooperative.New()
	defer other.Close()
	if err := other.Run(context.Background(), func(scope *cooperative.Scope) error {
		if !errors.Is(m.openAIAuth.checkClaudeTokensBeforeClient(context.Background(), scope), cooperative.ErrRestricted) {
			return errors.New("foreign credential check admitted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
