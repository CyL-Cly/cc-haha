package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestNativeModelPickerActionCancelPersistenceAndNextSharedRequest(t *testing.T) {
	var mu sync.Mutex
	var requests []json.RawMessage
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		response(w, "picker-next", "", nil, "SHARED MODEL NEXT")
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.Model = "sonnet"
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "native-first-model"
		env["ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES"] = "effort,max_effort"
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "native-second-model"
		env["ANTHROPIC_DEFAULT_OPUS_MODEL_SUPPORTED_CAPABILITIES"] = "effort,max_effort"
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(config, "settings.json")
	original := []byte(`{"model":"prior-settings-model","effortLevel":"medium","futureField":{"kept":true}}`)
	if err := os.WriteFile(settingsPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var picker *tui.ModelPicker
	var notices []string
	emit := func(event tui.Event) error {
		if event.Kind == tui.ModelPickerOpened {
			picker = event.ModelPicker
		}
		if event.Kind == tui.Notice {
			notices = append(notices, event.Text)
		}
		return nil
	}
	if err = s.InvokeAction(context.Background(), "command:model", emit); err != nil {
		t.Fatal(err)
	}
	if picker == nil || picker.Initial == nil || *picker.Initial != "sonnet" || picker.Effort == nil || *picker.Effort != "medium" || !picker.Standalone {
		t.Fatalf("raw Source initial state lost: %#v", picker)
	}
	if err = s.RespondModelPicker(context.Background(), picker.ID, tui.ModelPickerResult{Cancelled: true}, emit); err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || notices[0] != "Kept model as native-first-model" {
		t.Fatal("standalone Source cancel notice", notices)
	}
	if data, err := os.ReadFile(settingsPath); err != nil || !bytes.Equal(data, original) {
		t.Fatal("cancel mutated original settings", string(data), err)
	}
	if err = s.InvokeAction(context.Background(), "chat:modelPicker", emit); err != nil {
		t.Fatal(err)
	}
	if picker.Standalone {
		t.Fatal("hotkey incorrectly became command picker")
	}
	if err = s.RespondModelPicker(context.Background(), picker.ID, tui.ModelPickerResult{Cancelled: true}, emit); err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 {
		t.Fatal("hotkey cancel emitted a standalone message", notices)
	}
	picker = nil
	if err = s.Submit(context.Background(), tui.Input{Text: "/model", Mode: "prompt"}, emit); err != nil {
		t.Fatal(err)
	}
	if picker == nil || !picker.Standalone {
		t.Fatal("typed /model did not use the shared native picker")
	}
	if err = s.RespondModelPicker(context.Background(), picker.ID, tui.ModelPickerResult{Cancelled: true}, emit); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	count := len(requests)
	mu.Unlock()
	if count != 0 {
		t.Fatal("opening/cancelling menu queried model", count)
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "before selection"}, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.InvokeAction(context.Background(), "command:model", emit); err != nil {
		t.Fatal(err)
	}
	model, effort := "opus", "low"
	if err = s.RespondModelPicker(context.Background(), picker.ID, tui.ModelPickerResult{Model: &model, Effort: &effort, HasToggledEffort: true}, emit); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err = json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["model"] != "opus" || persisted["effortLevel"] != "low" || persisted["futureField"].(map[string]any)["kept"] != true {
		t.Fatal("actual settings did not retain raw alias/effort/unknown field", string(data))
	}
	if !strings.Contains(notices[len(notices)-1], "Set model to native-second-model with low effort") {
		t.Fatal("selected Source notice", notices)
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "after selection"}, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	captured := append([]json.RawMessage(nil), requests...)
	mu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("picker made another model consumer: requests=%d", len(captured))
	}
	var before, after map[string]any
	if err = json.Unmarshal(captured[0], &before); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(captured[1], &after); err != nil {
		t.Fatal(err)
	}
	if before["model"] != "native-first-model" || after["model"] != "native-second-model" {
		t.Fatalf("next SDK request did not consume selected model: before=%s after=%s", captured[0], captured[1])
	}
	if after["output_config"].(map[string]any)["effort"] != "low" {
		t.Fatalf("selected effort did not reach same request: %s", captured[1])
	}
	if !bytes.Contains(captured[1], []byte("before selection")) || !bytes.Contains(captured[1], []byte("SHARED MODEL NEXT")) {
		t.Fatal("model change replaced shared conversation", string(captured[1]))
	}
	restartOptions := o
	restartOptions.Arguments.Model = ""
	restartOptions.Arguments.SessionID = "fe210ac4-5364-4b5e-a0b8-e351b70e0a18"
	restarted, err := NewSession(context.Background(), restartOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close(context.Background())
	var restartPicker *tui.ModelPicker
	if err = restarted.InvokeAction(context.Background(), "command:model", func(event tui.Event) error {
		if event.Kind == tui.ModelPickerOpened {
			restartPicker = event.ModelPicker
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if restartPicker == nil || restartPicker.Initial == nil || *restartPicker.Initial != "opus" || restartPicker.Effort == nil || *restartPicker.Effort != "low" {
		t.Fatalf("new native host lost persisted Source alias/effort: %#v", restartPicker)
	}
	if err = restarted.Submit(context.Background(), tui.Input{Text: "fresh host after persisted selection"}, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	restartRequest := requests[len(requests)-1]
	mu.Unlock()
	if !bytes.Contains(restartRequest, []byte(`"model":"native-second-model"`)) || !bytes.Contains(restartRequest, []byte(`"effort":"low"`)) {
		t.Fatal("new native host did not use persisted model and effort", string(restartRequest))
	}
	if err = restarted.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = s.InvokeAction(context.Background(), "command:model", emit); err != nil {
		t.Fatal(err)
	}
	if picker.Initial == nil || *picker.Initial != "opus" {
		t.Fatal("picker reverse-inferred resolved model", picker.Initial)
	}
	if err = s.RespondModelPicker(context.Background(), picker.ID, tui.ModelPickerResult{Model: nil, Effort: &effort}, emit); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	var final map[string]any
	if err = json.Unmarshal(data, &final); err != nil {
		t.Fatal(err)
	}
	if _, found := final["model"]; found {
		t.Fatal("Source null should delete user setting", string(data))
	}
	if final["futureField"].(map[string]any)["kept"] != true {
		t.Fatal("default deleted unrelated user setting", string(data))
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "after default"}, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	last := requests[len(requests)-1]
	finalCount := len(requests)
	mu.Unlock()
	if !bytes.Contains(last, []byte(`"model":"native-first-model"`)) {
		t.Fatal("null retained prior CLI/raw override", string(last))
	}
	if finalCount != 4 {
		t.Fatal("opening/selecting/restarting menus created another model consumer", finalCount)
	}
}
