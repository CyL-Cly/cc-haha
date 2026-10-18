package clibootstrap

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestServerSettingsUsesActualGlobalAndCompleteSourcePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, user, project, local, policy string
		want                               bool
	}{
		{"global-fallback", `{}`, `{}`, `{}`, `{}`, false},
		{"user", `{"respectGitignore":true}`, `{}`, `{}`, `{}`, true},
		{"project", `{"respectGitignore":false}`, `{"respectGitignore":true}`, `{}`, `{}`, true},
		{"local", `{"respectGitignore":false}`, `{"respectGitignore":true}`, `{"respectGitignore":false}`, `{}`, false},
		{"managed-policy", `{"respectGitignore":false}`, `{"respectGitignore":true}`, `{"respectGitignore":false}`, `{"respectGitignore":true}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			home, config, work, managed := filepath.Join(root, "home"), filepath.Join(root, "config"), filepath.Join(root, "work"), filepath.Join(root, "managed")
			write := func(path, text string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(config, ".claude.json"), `{"respectGitignore":false,"future":{"keep":true}}`)
			write(filepath.Join(config, "settings.json"), tc.user)
			write(filepath.Join(work, ".claude", "settings.json"), tc.project)
			write(filepath.Join(work, ".claude", "settings.local.json"), tc.local)
			write(filepath.Join(managed, "managed-settings.json"), tc.policy)
			if err := os.MkdirAll(home, 0700); err != nil {
				t.Fatal(err)
			}
			n, err := newIsolatedProcessNativeBoundary(NativeProcessOptions{Environment: map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config, "ANTHROPIC_BASE_URL": "http://127.0.0.1:1"}, CWD: work, Platform: runtime.GOOS, ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			if err != nil {
				t.Fatal(err)
			}
			settings, err := NewNativeProcessSettings(n, runtime.GOOS, DefaultState())
			if err != nil {
				t.Fatal(err)
			}
			defer settings.Close(context.Background())
			if got, err := settings.RespectGitignore(context.Background()); err != nil || got != tc.want {
				t.Fatalf("respectGitignore=%v want=%v err=%v", got, tc.want, err)
			}
			before, _ := os.ReadFile(filepath.Join(config, ".claude.json"))
			if string(before) != `{"respectGitignore":false,"future":{"keep":true}}` {
				t.Fatal("read-only route changed global user state", string(before))
			}
		})
	}
}
