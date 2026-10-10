package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"os"
	"path/filepath"
	"testing"
)

func TestTypedModelInfoConsumesActualSourcePickerCallbackState(t *testing.T) {
	selected := "opus"
	rows := []typedModelSourceRow{{Name: "original-picker", Args: "", SelectModel: &selected, Settings: map[string]any{"model": "sonnet", "future": true}, State: map[string]any{"mainLoopModel": "sonnet", "mainLoopModelForSession": nil, "fastMode": false}}, {Name: "typed-info-after-picker", Args: "info", Settings: map[string]any{}, State: map[string]any{}, ContinueStore: true}}
	// Original common info token is current, not the custom-model name info.
	rows[1].Args = "current"
	want := runTypedModelSource(t, rows, "")
	o := nativeSessionOptions(t, "http://127.0.0.1:43210")
	o.Interactive = true
	o.Arguments.Model = "sonnet"
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	_ = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"model":"sonnet","future":true}`), 0600)
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close(context.Background())
	var picker *tui.ModelPicker
	if e = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "/model"}, func(event tui.Event) error {
		if event.Kind == tui.ModelPickerOpened {
			picker = event.ModelPicker
		}
		return nil
	}); e != nil || picker == nil {
		t.Fatal(e)
	}
	if e = s.RespondModelPicker(context.Background(), picker.ID, tui.ModelPickerResult{Model: &selected}, func(tui.Event) error { return nil }); e != nil {
		t.Fatal(e)
	}
	var notice string
	if e = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "/model current"}, func(event tui.Event) error {
		if event.Kind == tui.Notice {
			notice = event.Text
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if notice != want[1].Notice {
		t.Fatal("actual original wrapper selection -> typed info", notice, want[1].Notice)
	}
}
