package clisettings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type failedPermissionWrite struct {
	OSFileSystem
	writes int
}

func (f *failedPermissionWrite) WriteFlush(string, []byte) error {
	f.writes++
	return errors.New("fixture disk unavailable")
}

func TestPermissionEditingPhysicalFailurePreservesFileAndCacheAndDoesNotStartIgnore(t *testing.T) {
	home := t.TempDir()
	config, cwd := filepath.Join(home, ".claude"), filepath.Join(home, "project")
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(cwd, 0700)
	path := filepath.Join(config, "settings.json")
	old := []byte(`{"future":{"keep":true},"model":"fixture"}`)
	_ = os.WriteFile(path, old, 0600)
	state, _ := NewState(func() float64 { return 1700000000000 })
	cached := protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: quoted("before")})
	state.SetCachedSettingsForSource("userSettings", &cached)
	fs := &failedPermissionWrite{}
	effects := []Effect{}
	service, e := NewService(Options{State: state, ConfigHome: config, OriginalCWD: cwd, Profile: NativeProcessProfile, PID: 17, Now: func() float64 { return 1700000000000 }, FS: fs, Observe: func(effect Effect) { effects = append(effects, effect) }})
	if e != nil {
		t.Fatal(e)
	}
	var patch protocol.JSONObject
	_ = patch.UnmarshalJSON(json.RawMessage(`{"permissions":{"allow":["WebFetch(domain:example.org)"]}}`))
	started := false
	result, e := service.UpdatePermissionEditingSource("userSettings", patch, PermissionEditingBindings{StartLocalIgnore: func(string, string) error { started = true; return nil }})
	if e != nil || result.Error == nil || fs.writes != 2 || started {
		t.Fatalf("writer contract result%+v err%v writes%d ignore%v", result, e, fs.writes, started)
	}
	actual, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(actual, old) {
		t.Fatalf("file changed %s %v", actual, e)
	}
	value, ok := state.GetCachedSettingsForSource("userSettings")
	if !ok || value.String("model") != "before" {
		t.Fatal("failed writer leaked cache mutation")
	}
	marked := false
	for _, effect := range effects {
		if effect.Kind == "reset" {
			t.Fatal("failed writer reset cache")
		}
		marked = marked || effect.Kind == "mark"
	}
	if !marked {
		t.Fatal("original mark-before-write order absent")
	}
}

func TestLocalIgnoreProcessFailureLogsAndStillWritesOriginalGlobalPattern(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "repo")
	_ = os.MkdirAll(filepath.Join(cwd, ".git"), 0700)
	logged := []error{}
	service, e := NewLocalIgnore(LocalIgnoreOptions{Home: home, Stat: os.Stat, ReadFile: os.ReadFile, MkdirAll: func(path string) error { return os.MkdirAll(path, 0777) }, WriteFile: func(path string, data []byte) error { return os.WriteFile(path, data, 0666) }, AppendFile: func(string, []byte) error { t.Fatal("unexpected append"); return nil }, RunGit: func(context.Context, string, []string) (int, error) {
		return 1, errors.New("fixture git spawn failure")
	}, LogError: func(e error) { logged = append(logged, e) }})
	if e != nil {
		t.Fatal(e)
	}
	if e = service.Run(context.Background(), ".claude/settings.local.json", cwd); e != nil {
		t.Fatal(e)
	}
	actual, e := os.ReadFile(filepath.Join(home, ".config/git/ignore"))
	if e != nil || string(actual) != "**/.claude/settings.local.json\n" || len(logged) != 1 {
		t.Fatalf("ignore failure contract%q %v logs%d", actual, e, len(logged))
	}
}
