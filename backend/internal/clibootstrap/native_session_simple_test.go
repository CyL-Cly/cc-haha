package clibootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeSimpleSkipsSessionStartAndEndBeforeProcessEnvironmentFiles(t *testing.T) {
	var marker string
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1/no-model", func(n *NativeBoundary) {
		n.Environment["CLAUDE_CODE_SIMPLE"] = "1"
		marker = filepath.Join(n.CWD, "must-not-run")
		command := "printf violated > '" + marker + "'"
		settings := map[string]any{"hooks": map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}}, "SessionEnd": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}}}}
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), rawMain(settings), 0600); err != nil {
			t.Fatal(err)
		}
	}, nil)
	if err := m.ShutdownSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("--bare executed actual hook", err)
	}
	if _, err := os.Stat(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "session-env")); !os.IsNotExist(err) {
		t.Fatal("bare Hook env-file allocation", err)
	}
}
