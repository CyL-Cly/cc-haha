package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalConfigOutputStylePickerLanguageSaveRevertAndActualNextPrompt(t *testing.T) {
	var systems []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		systems = append(systems, string(obj(body).Get("system")))
		response(w, "config-content", "", nil, "CONFIG_CONTENT_ACTUAL_DONE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.SystemPrompt = ""
	o.Interactive = true
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	style := filepath.Join(config, "output-styles", "owned.md")
	_ = os.MkdirAll(filepath.Dir(style), 0700)
	_ = os.WriteFile(style, []byte("---\nname: Fixture Style\ndescription: Actual custom style description\n---\nCONFIG_CONTENT_STYLE_PROMPT_389"), 0600)
	user := filepath.Join(config, "settings.json")
	local := filepath.Join(o.WorkDir, ".claude", "settings.local.json")
	_ = os.MkdirAll(filepath.Dir(local), 0700)
	_ = os.WriteFile(user, []byte(`{"language":"Deutsch","futureUser":{"keep":true}}`), 0600)
	_ = os.WriteFile(local, []byte(`{"outputStyle":"Explanatory","futureLocal":{"keep":true}}`), 0600)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var panel *tui.LocalPanel
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		return nil
	}
	open := func() {
		t.Helper()
		if _, err := s.InvokeTerminalCommand(context.Background(), "config", "", emit); err != nil {
			t.Fatal(err)
		}
	}
	act := func(action, row string, fields map[string]string) {
		t.Helper()
		if err := s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: action, Row: row, Fields: fields}, emit); err != nil {
			t.Fatal(err)
		}
	}
	open()
	parent := panel.ID
	act("edit-config", "outputStyle", nil)
	if panel.ID != parent || panel.Title != "Preferred output style" || len(panel.Fields) != 0 || panel.SelectedRow != "Explanatory" || len(panel.Rows) != 4 {
		t.Fatal(panel)
	}
	if panel.Rows[0].ID != "default" || panel.Rows[0].Label != "Default" || panel.Rows[3].ID != "Fixture Style" || panel.Rows[3].Detail != "Actual custom style description" {
		t.Fatal(panel.Rows)
	}
	act("config-back", "", nil)
	data, _ := os.ReadFile(local)
	if obj(data).String("outputStyle") != "Explanatory" {
		t.Fatal("child cancel wrote style", string(data))
	}
	act("edit-config", "outputStyle", nil)
	act("select-output-style", "Fixture Style", nil)
	if panel.Title != "Config" || panel.Submenu {
		t.Fatal(panel)
	}
	act("edit-config", "language", nil)
	act("save", "", map[string]string{"value": "\ufeff\u00a0 日本語 \r\n"})
	data, _ = os.ReadFile(user)
	if obj(data).String("language") != "日本語" || string(obj(obj(data).Get("futureUser")).Get("keep")) != "true" {
		t.Fatal(string(data))
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "first actual style-language request"}, emit); err != nil {
		t.Fatal(err)
	}
	act("close", "", nil)
	data, _ = os.ReadFile(local)
	if obj(data).String("outputStyle") != "Explanatory" || string(obj(obj(data).Get("futureLocal")).Get("keep")) != "true" {
		t.Fatal(string(data))
	}
	data, _ = os.ReadFile(user)
	if obj(data).String("language") != "Deutsch" {
		t.Fatal(string(data))
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "actual prompt after outer revert"}, emit); err != nil {
		t.Fatal(err)
	}
	// Source retains section-cache values across Config writes. A new Session
	// starts a fresh section cache and consumes the actual restored settings.
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	o.Arguments.SessionID = "95d45dde-2383-4f09-af56-0b7a845b0380"
	s, err = NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err = s.Submit(context.Background(), tui.Input{Text: "actual prompt in next session"}, emit); err != nil {
		t.Fatal(err)
	}
	open()
	act("edit-config", "language", nil)
	act("save", "", map[string]string{"value": "\ufeff\u00a0 \t\r\n"})
	act("commit-config", "", nil)
	data, _ = os.ReadFile(user)
	if obj(data).Get("language") != nil || string(obj(obj(data).Get("futureUser")).Get("keep")) != "true" {
		t.Fatal(string(data))
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "actual prompt after language reset"}, emit); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	o.Arguments.SessionID = "baae1417-aea1-419b-91b1-d778fe37c526"
	s, err = NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err = s.Submit(context.Background(), tui.Input{Text: "actual next session after language reset"}, emit); err != nil {
		t.Fatal(err)
	}
	if len(systems) != 5 || !strings.Contains(systems[0], "CONFIG_CONTENT_STYLE_PROMPT_389") || !strings.Contains(systems[0], "Always respond in 日本語") || !strings.Contains(systems[1], "CONFIG_CONTENT_STYLE_PROMPT_389") || !strings.Contains(systems[1], "Always respond in 日本語") || strings.Contains(systems[2], "CONFIG_CONTENT_STYLE_PROMPT_389") || !strings.Contains(systems[2], "Always respond in Deutsch") || !strings.Contains(systems[3], "Always respond in Deutsch") || strings.Contains(systems[4], "# Language") {
		for i, value := range systems {
			t.Errorf("request %d: custom style=%v, Japanese=%v, German=%v, language section=%v", i, strings.Contains(value, "CONFIG_CONTENT_STYLE_PROMPT_389"), strings.Contains(value, "Always respond in 日本語"), strings.Contains(value, "Always respond in Deutsch"), strings.Contains(value, "# Language"))
		}
	}
}

func TestTerminalConfigContentPickerActualOriginalCallbacks(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-config-content.fixture.ts")
	cmd := exec.CommandContext(context.Background(), bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_CONFIG_CONTENT_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	values := []string{"", " \t\r\n", "\ufeff\u00a0日本語\u2028", " \u0085Deutsch\u0085 ", "Español", "中文"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "languages": values, "styles": json.RawMessage(`{"default":null,"Fixture Style":{"name":"Fixture Style","description":"Actual custom style description"}}`)}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source %v %.5000s", err, output)
	}
	var source struct {
		CacheTrace [][]string
		Languages  []*string
		Options    []struct{ Label, Value, Description string }
		Writes     []struct {
			Source string
			Patch  map[string]string
		}
	}
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	for i, value := range values {
		got := terminalConfigLanguage(value)
		if source.Languages[i] == nil && got != "" || source.Languages[i] != nil && *source.Languages[i] != got {
			t.Fatal(i, got, string(output))
		}
	}
	if len(source.Options) != 2 || source.Options[0].Label != "Default" || source.Options[1].Description != "Actual custom style description" || len(source.Writes) != 3 || source.Writes[0].Source != "localSettings" || source.Writes[0].Patch["outputStyle"] != "Fixture Style" || source.Writes[1].Source != "userSettings" || source.Writes[1].Patch["language"] != "日本語" || len(source.Writes[2].Patch) != 0 {
		t.Fatal(string(output))
	}
	if len(source.CacheTrace) != 3 || strings.Join(source.CacheTrace[0], ",") != "日本語,Fixture Style" || strings.Join(source.CacheTrace[1], ",") != "日本語,Fixture Style" || strings.Join(source.CacheTrace[2], ",") != "Deutsch,Explanatory" {
		t.Fatal("original cache lifecycle changed", string(output))
	}
}
