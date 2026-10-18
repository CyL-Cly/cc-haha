package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
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

func TestTerminalConfigNestedModelActualAppStatePersistenceRevertAndNextRequest(t *testing.T) {
	var models []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		models = append(models, obj(body).String("model"))
		response(w, "config-model", "", nil, "CONFIG_MODEL_ACTUAL_DONE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.Arguments.Model = "sonnet"
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "private-sonnet-model"
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "private-opus-model"
		env["ANTHROPIC_DEFAULT_OPUS_MODEL_SUPPORTED_CAPABILITIES"] = "effort,max_effort"
	}
	path := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	if err := os.WriteFile(path, []byte(`{"model":"original-file-model","effortLevel":"medium","futureUser":{"keep":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var panel *tui.LocalPanel
	var picker *tui.ModelPicker
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		if event.ModelPicker != nil {
			copy := *event.ModelPicker
			picker = &copy
		}
		return nil
	}
	open := func() {
		t.Helper()
		if _, err := s.InvokeTerminalCommand(context.Background(), "config", "", emit); err != nil {
			t.Fatal(err)
		}
	}
	act := func(name, row string) {
		t.Helper()
		if err := s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: name, Row: row}, emit); err != nil {
			t.Fatal(err)
		}
	}
	selectModel := func(cancel bool) {
		t.Helper()
		act("edit-config", "model")
		if picker == nil || picker.Standalone {
			t.Fatal(picker)
		}
		opus := "opus"
		high := "high"
		if err := s.RespondModelPicker(context.Background(), picker.ID, tui.ModelPickerResult{Cancelled: cancel, Model: &opus, Effort: &high, HasToggledEffort: !cancel}, emit); err != nil {
			t.Fatal(err)
		}
	}
	open()
	id := panel.ID
	selectModel(true)
	if panel.ID != id {
		t.Fatal("cancel replaced parent")
	}
	data, _ := os.ReadFile(path)
	if obj(data).String("model") != "original-file-model" {
		t.Fatal("cancel persisted", string(data))
	}
	selectModel(false)
	if panel.ID != id {
		t.Fatal("selection replaced parent")
	}
	data, _ = os.ReadFile(path)
	if obj(data).String("model") != "opus" || obj(data).String("effortLevel") != "high" {
		t.Fatal(string(data))
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "first real query"}, emit); err != nil {
		t.Fatal(err)
	}
	act("close", "")
	data, _ = os.ReadFile(path)
	// Source's real onChangeAppState writes the restored session base, not the
	// old disk model. ModelPicker effort is intentionally not a Config revert key.
	if obj(data).String("model") != "sonnet" || obj(data).String("effortLevel") != "high" || obj(obj(data).Get("futureUser")).Get("keep") == nil {
		t.Fatal(string(data))
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "query after config escape"}, emit); err != nil {
		t.Fatal(err)
	}
	open()
	selectModel(false)
	act("commit-config", "")
	if err = s.Submit(context.Background(), tui.Input{Text: "query after config commit"}, emit); err != nil {
		t.Fatal(err)
	}
	if strings.Join(models, ",") != "private-opus-model,private-sonnet-model,private-opus-model" {
		t.Fatal(models)
	}
	if err = s.RespondModelPicker(context.Background(), picker.ID, tui.ModelPickerResult{Cancelled: true}, emit); err == nil {
		t.Fatal("stale child callback admitted")
	}
}
func TestTerminalConfigSearchAndModelRevertActualOriginalCallbacks(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-config-navigation.fixture.ts")
	cmd := exec.CommandContext(context.Background(), bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_CONFIG_NAVIGATION_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "base": "sonnet", "session": "session-plan-model", "chosen": "opus", "user": map[string]any{"model": "original-file-model", "effortLevel": "high", "futureUser": true}, "global": map[string]any{}, "rows": []any{map[string]any{"id": "model", "label": "Model"}, map[string]any{"id": "autoCompactEnabled", "label": "Auto-compact"}}, "steps": []any{map[string]any{"action": "settings:search"}, map[string]any{"key": "A"}, map[string]any{"key": "return"}, map[string]any{"action": "select:previous"}, map[string]any{"key": "escape"}, map[string]any{"key": "escape"}}}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source %v %.7000s", err, output)
	}
	var source struct {
		Traces []struct {
			Query  string
			Search bool
			IDs    []string
		}
		SelectedUser, RevertedUser map[string]any
		SelectedApp, RevertedApp   map[string]any
	}
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	if len(source.Traces) != 6 || source.Traces[1].Query != "A" || !source.Traces[1].Search || len(source.Traces[1].IDs) != 1 || source.Traces[1].IDs[0] != "autoCompactEnabled" || source.Traces[2].Search || !source.Traces[3].Search || source.Traces[4].Query != "" || !source.Traces[4].Search || source.Traces[5].Search {
		t.Fatal(string(output))
	}
	if source.SelectedUser["model"] != "opus" || source.SelectedApp["mainLoopModelForSession"] != nil || source.RevertedUser["model"] != "sonnet" || source.RevertedUser["effortLevel"] != "high" || source.RevertedApp["mainLoopModelForSession"] != "session-plan-model" || source.RevertedApp["fastMode"] != true {
		t.Fatal(string(output))
	}
}

func TestTerminalConfigModelPickerUsesActualCachedSubscriberCatalogWithoutOAuth(t *testing.T) {
	for _, kind := range []string{"pro", "max", "team"} {
		t.Run(kind, func(t *testing.T) {
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			o.Interactive = true
			o.Arguments.Model = ""
			for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
				env["ANTHROPIC_API_KEY"] = ""
				env["NODE_ENV"] = "production"
			}
			tier := ""
			if kind == "team" {
				tier = "default_claude_max_5x"
			}
			private := raw(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fake-never-http", "refreshToken": "fake-never-refresh", "expiresAt": 2000000000000, "scopes": []string{"user:profile", "user:inference"}, "subscriptionType": kind, "rateLimitTier": tier}})
			reads := 0
			o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { reads++; return private, nil }
			config := o.Environment["CLAUDE_CONFIG_DIR"]
			_ = os.MkdirAll(config, 0700)
			_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"oauthAccount": map[string]any{"emailAddress": "owned@example.invalid", "organizationName": "Owned Fixture Org"}}), 0600)
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			var panel *tui.LocalPanel
			var picker *tui.ModelPicker
			emit := func(event tui.Event) error {
				if event.LocalPanel != nil {
					copy := *event.LocalPanel
					panel = &copy
				}
				if event.ModelPicker != nil {
					copy := *event.ModelPicker
					picker = &copy
				}
				return nil
			}
			if _, err = s.InvokeTerminalCommand(context.Background(), "config", "", emit); err != nil {
				t.Fatal(err)
			}
			before := reads
			if err = s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: panel.ID, Action: "edit-config", Row: "model"}, emit); err != nil {
				t.Fatal(err)
			}
			if picker == nil || picker.Standalone || len(picker.Options) < 3 || picker.Options[0].Value != nil || !strings.Contains(picker.Options[0].Label, "Default") {
				t.Fatal("cached subscriber actual catalog absent", picker)
			}
			joined := string(raw(picker.Options))
			if !strings.Contains(joined, "Sonnet") || !strings.Contains(joined, "Opus") || strings.Contains(joined, "fake-never") {
				t.Fatal(joined)
			}
			if err = s.RespondModelPicker(context.Background(), picker.ID, tui.ModelPickerResult{Cancelled: true}, emit); err != nil {
				t.Fatal(err)
			}
			if reads != before {
				t.Fatal("picker recreated credential cache", before, reads)
			}
		})
	}
}
