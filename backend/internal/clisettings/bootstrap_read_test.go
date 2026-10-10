package clisettings

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Source getSettingsFilePathForSource / getSettingsForSourceUncached read the
// original root path directly. Git-ignore work belongs only to the local write.
func TestBootstrapPersistenceSettingsReadInsideRealGitRepository(t *testing.T) {
	for _, kind := range []string{"directory", "worktree-file"} {
		t.Run(kind, func(t *testing.T) {
			service, state, _ := testService(t, nil, nil)
			root := service.options.OriginalCWD
			if kind == "directory" {
				if e := os.Mkdir(filepath.Join(root, ".git"), 0700); e != nil {
					t.Fatal(e)
				}
			} else {
				if e := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: original-private.git\n"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			path, e := service.Path("localSettings", "")
			if e != nil || path != filepath.Join(root, ".claude", "settings.local.json") {
				t.Fatal("read path inspected .git", path, e)
			}
			value, e := service.GetEditableSource("localSettings", "")
			if e != nil || value != nil {
				t.Fatal("absent local settings misclassified", value, e)
			}
			if _, e = os.Stat(filepath.Dir(path)); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("read created settings directory", e)
			}
			state.ResetSettingsCache()
			if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
				t.Fatal(e)
			}
			original := []byte(`{"model":"original-model","env":{"DECLARED":"original-value"},"future":{"raw":"retained"}}`)
			if e = os.WriteFile(path, original, 0600); e != nil {
				t.Fatal(e)
			}
			value, e = service.GetEditableSource("localSettings", "")
			if e != nil || value == nil || value.String("model") != "original-model" || string(value.Get("future")) != `{"raw":"retained"}` {
				t.Fatal(value, e)
			}
			_, e = service.UpdateEditableSource("localSettings", "", Patch{Object: object(t, `{"model":"changed"}`)})
			var cap *CapabilityError
			if !errors.As(e, &cap) || cap.Capability != "local settings write Git-ignore continuation" {
				t.Fatal("unported local write admitted", e)
			}
			after, e := os.ReadFile(path)
			if e != nil || string(after) != string(original) || state.ConsumeInternalWrite(path, 5000) {
				t.Fatal("guard caused write/cache marker", string(after), e)
			}
		})
	}
}

func TestBootstrapPersistenceSettingsLocalOverrideAndAncestorRepository(t *testing.T) {
	service, state, _ := testService(t, nil, nil)
	home := service.options.OriginalCWD
	override := filepath.Join(home, "nested", "original-root")
	if e := os.Mkdir(filepath.Join(home, ".git"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(override, 0700); e != nil {
		t.Fatal(e)
	}
	path, e := service.Path("localSettings", override)
	if e != nil || path != filepath.Join(override, ".claude", "settings.local.json") {
		t.Fatal(path, e)
	}
	value, e := service.GetEditableSource("localSettings", override)
	if e != nil || value != nil {
		t.Fatal(value, e)
	}
	_, e = service.UpdateEditableSource("localSettings", override, Patch{Object: object(t, `{"model":"original-model"}`)})
	var cap *CapabilityError
	if !errors.As(e, &cap) || cap.Capability != "local settings write Git-ignore continuation" {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Dir(path)); !errors.Is(e, os.ErrNotExist) || state.ConsumeInternalWrite(path, 5000) {
		t.Fatal("ancestor write guard effects", e)
	}
	if _, e = service.Path("localSettings", "relative"); e == nil {
		t.Fatal("unresolved original path admitted")
	}
}
