package computerusemcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/computeruse"
	"github.com/NanmiCoder/cc-haha/backend/internal/curepl"
)

func boundFixture(t *testing.T) (*Binder, *computeruse.Service) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	config := filepath.Join(home, ".claude")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	service, err := computeruse.New(computeruse.Options{HomeDir: home, ConfigDir: config, AppRoot: root, WorkDir: root, Platform: "darwin", Environment: map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	if err = service.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	native, err := computeruse.NewNativeTools(computeruse.NativeToolsOptions{Service: service, SessionID: "actual-fixture-sid", NativeCall: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		t.Error("invalid MCP input dispatched native helper")
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	kernel, err := curepl.NewRuntime(curepl.RuntimeOptions{Executable: filepath.Join(root, "must-not-launch"), TempDir: root, Platform: "darwin"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(native, kernel)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	return b, service
}
func TestMacAdvertisesOriginalTwoToolsAndRespectsGlobalDisable(t *testing.T) {
	b, service := boundFixture(t)
	tools, err := b.Tools(context.Background())
	if err != nil || len(tools) != 2 || tools[0].String("name") != "js" || tools[1].String("name") != "js_reset" {
		t.Fatal(tools, err)
	}
	if err = service.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":false}`)); err != nil {
		t.Fatal(err)
	}
	tools, err = b.Tools(context.Background())
	if err != nil || len(tools) != 0 {
		t.Fatal(tools, err)
	}
	result := b.Call(context.Background(), "js", json.RawMessage(`{"code":"nodeRepl.write(1)"}`))
	if !result.IsError || result.Content[0].Text != "Computer Use JavaScript is disabled or cancelled." {
		t.Fatal(result)
	}
}
func TestInvalidInputsDoNotStartKernelOrNativeAction(t *testing.T) {
	b, _ := boundFixture(t)
	for _, raw := range []string{"null", "[]", `{}`, `{"code":null}`, `{"code":"1","timeout_ms":0}`, `{"code":"1","timeout_ms":60001}`, `{"code":"1","timeout_ms":1.5}`, `{"code":"1","timeout_ms":"1"}`, `{"code":"1","title":null}`, `{"code":"1","future":true}`, `{"code":"` + strings.Repeat("a", curepl.MaxCodeBytes+1) + `"}`} {
		got := b.Call(context.Background(), "js", json.RawMessage(raw))
		if !got.IsError || strings.Contains(got.Content[0].Text, "startup") {
			t.Fatal(raw[:min(len(raw), 120)], got)
		}
	}
	for _, raw := range []string{"null", "[]", `{"future":1}`} {
		got := b.Call(context.Background(), "js_reset", json.RawMessage(raw))
		if !got.IsError || got.Content[0].Text != "js_reset takes an empty object." {
			t.Fatal(got)
		}
	}
	got := b.Call(context.Background(), "js_reset", json.RawMessage(`{}`))
	if got.IsError || len(got.Content) != 1 || got.Content[0].Text != "Computer Use JavaScript reset. Select an app again to continue." {
		t.Fatal(got)
	}
}
