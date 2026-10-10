package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"os"
	"path/filepath"
	"testing"
)

func TestTypedModelFastNoticeUsesActualSourceInitialSettingAndCachedOrg(t *testing.T) {
	row := typedModelSourceRow{Name: "actual-fast-initial", Args: "opus", Settings: map[string]any{"model": "opus", "fastMode": true, "future": true}, State: map[string]any{"mainLoopModel": "opus", "mainLoopModelForSession": nil}, Env: map[string]string{"ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-4-6"}, Global: map[string]any{"penguinModeOrgEnabled": true}, InitializeFast: true}
	want := runTypedModelSource(t, []typedModelSourceRow{row}, "")
	if want[0].State["fastMode"] != true {
		t.Fatal("actual Source initializer did not admit cached org profile", want[0])
	}
	o := nativeSessionOptions(t, "http://127.0.0.1:43210")
	o.Arguments.Model = "opus"
	o.Interactive = true
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "claude-opus-4-6"
		delete(env, "CLAUDE_CODE_DISABLE_FAST_MODE")
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"model":"opus","fastMode":true,"future":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{"penguinModeOrgEnabled":true,"future":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var notice string
	if err = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "/model opus"}, func(e tui.Event) error {
		if e.Kind == tui.Notice {
			notice = e.Text
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if notice != want[0].Notice {
		t.Fatalf("actual Source fast initial/state notice %q Go %q", want[0].Notice, notice)
	}
}
