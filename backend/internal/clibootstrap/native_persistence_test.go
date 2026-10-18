package clibootstrap

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativePersistenceUsesCompleteSourceSettingsAndStrictZero(t *testing.T) {
	for _, tc := range []struct {
		name, user, project, policy string
		want                        bool
	}{
		{"absent", `{}`, `{}`, `{}`, true},
		{"user-zero", `{"cleanupPeriodDays":0}`, `{}`, `{}`, false},
		{"negative-zero", `{"cleanupPeriodDays":-0}`, `{}`, `{}`, false},
		{"project-zero", `{"cleanupPeriodDays":30}`, `{"cleanupPeriodDays":0}`, `{}`, false},
		{"policy-overrides-zero", `{"cleanupPeriodDays":0}`, `{"cleanupPeriodDays":0}`, `{"cleanupPeriodDays":7}`, true},
		{"policy-zero", `{"cleanupPeriodDays":30}`, `{"cleanupPeriodDays":15}`, `{"cleanupPeriodDays":0}`, false},
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
			write(filepath.Join(config, "settings.json"), tc.user)
			write(filepath.Join(work, ".claude", "settings.json"), tc.project)
			write(filepath.Join(managed, "managed-settings.json"), tc.policy)
			if err := os.MkdirAll(home, 0700); err != nil {
				t.Fatal(err)
			}
			n, err := newIsolatedProcessNativeBoundary(NativeProcessOptions{Environment: map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config}, CWD: work, Platform: runtime.GOOS, ManagedRoot: managed, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
			if err != nil {
				t.Fatal(err)
			}
			settings, err := NewNativeProcessSettings(n, runtime.GOOS, DefaultState())
			if err != nil {
				t.Fatal(err)
			}
			defer settings.Close(context.Background())
			got, err := settings.PersistSession(context.Background())
			if err != nil || got != tc.want {
				t.Fatalf("persist=%v want=%v err=%v", got, tc.want, err)
			}
			if before, err := os.ReadFile(filepath.Join(config, "settings.json")); err != nil || string(before) != tc.user {
				t.Fatal("read changed user settings", string(before), err)
			}
		})
	}
}
