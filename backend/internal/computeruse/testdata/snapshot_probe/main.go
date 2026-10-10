// snapshot_probe is a private native integration fixture. It uses the actual
// Go daemon service and signed helper; it never loads providers or a model.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/computeruse"
)

func main() {
	root := os.Getenv("CU_PROBE_PRIVATE_ROOT")
	if root == "" || !filepath.IsAbs(root) || !strings.HasPrefix(root, "/private/tmp/cu-locator-") {
		panic("explicit disposable CU probe root required")
	}
	environment := map[string]string{}
	for _, entry := range os.Environ() {
		if key, value, ok := strings.Cut(entry, "="); ok {
			environment[key] = value
		}
	}
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	service, err := computeruse.New(computeruse.Options{HomeDir: environment["HOME"], ConfigDir: environment["CLAUDE_CONFIG_DIR"], WorkDir: root, TempDir: environment["TMPDIR"], Executable: executable, Environment: environment, Platform: "darwin", Arch: "arm64"})
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	defer service.Close(context.Background())
	if err = service.UpdateConfig(ctx, json.RawMessage(`{"enabled":true,"grantFlags":{"systemKeyCombos":true}}`)); err != nil {
		panic(err)
	}
	// The only target is the existing, authorized private desktop test app.
	raw, err := service.Call(ctx, "private-locator-proof", "get_app_state", json.RawMessage(`{"app":"com.claude-code-haha.desktop","disableDiff":true}`))
	receipt := map[string]any{"stateError": errorText(err)}
	if err == nil {
		var state map[string]json.RawMessage
		if err = json.Unmarshal(raw, &state); err != nil {
			panic(err)
		}
		var text string
		_ = json.Unmarshal(state["axText"], &text)
		if err = os.WriteFile(filepath.Join(root, "snapshot.ax.txt"), []byte(text), 0600); err != nil {
			panic(err)
		}
		composer := regexp.MustCompile(`(?m)^\s*(g[1-9][0-9]*:[0-9]+) text entry area \(settable, string\)`).FindStringSubmatch(text)
		if len(composer) == 2 {
			receipt["composerHandle"] = composer[1]
			// Mutating focus is opt-in. The investigation defaults to read-only.
			if environment["CU_PROBE_CLICK_COMPOSER"] == "1" {
				payload, _ := json.Marshal(map[string]any{"app": "com.claude-code-haha.desktop", "index": composer[1]})
				_, clickError := service.Call(ctx, "private-locator-proof", "click", payload)
				receipt["clickError"] = errorText(clickError)
			}
		}
	}
	encoded, _ := json.MarshalIndent(receipt, "", "  ")
	if err = os.WriteFile(filepath.Join(root, "receipt.json"), encoded, 0600); err != nil {
		panic(err)
	}
}

func errorText(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}
