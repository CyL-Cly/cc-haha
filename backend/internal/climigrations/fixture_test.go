package climigrations

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixtureInput struct {
	Home, Name                     string
	Global, User, Local, Changelog *string
	Env                            map[string]string
	Override                       *string
	Fault, Schema                  string
	Twice                          bool
	FaultStart                     int
}
type fixtureFS struct {
	cliglobalconfig.OSFileSystem
	input       fixtureInput
	events      *[]Event
	gate        <-chan struct{}
	faultWrites *int
}

func (f fixtureFS) Stat(path string) (cliglobalconfig.FileStat, error) {
	if filepath.Base(path) == ".git" && !strings.HasPrefix(path, f.input.Home+string(os.PathSeparator)) {
		return cliglobalconfig.FileStat{}, os.ErrNotExist
	}
	return f.OSFileSystem.Stat(path)
}
func (f fixtureFS) WriteFlush(path string, data []byte, mode *os.FileMode) error {
	*f.events = append(*f.events, Event{"kind": "write", "path": short(f.input.Home, path), "rawBase64": base64.StdEncoding.EncodeToString(data)})
	if f.input.Fault == "global" {
		*f.faultWrites++
		start := f.input.FaultStart
		if start == 0 {
			start = 1
		}
		if *f.faultWrites >= start {
			return fmt.Errorf("private fixture write fault")
		}
	}
	return f.OSFileSystem.WriteFlush(path, data, mode)
}

type fixtureSettingsFS struct {
	clisettings.OSFileSystem
	input fixtureInput
}

func (f fixtureSettingsFS) Stat(path string) (os.FileInfo, error) {
	if filepath.Base(path) == ".git" && !strings.HasPrefix(path, f.input.Home+string(os.PathSeparator)) {
		return nil, os.ErrNotExist
	}
	return f.OSFileSystem.Stat(path)
}
func (f fixtureSettingsFS) WriteFlush(path string, data []byte) error {
	if f.input.Fault == "user" && strings.Contains(path, "settings.json") || f.input.Fault == "local" && strings.Contains(path, "settings.local.json") {
		return fmt.Errorf("private fixture write fault")
	}
	return f.OSFileSystem.WriteFlush(path, data)
}

type fixtureAsyncFS struct {
	OSFileSystem
	input  fixtureInput
	events *[]Event
	gate   <-chan struct{}
}

func (f fixtureAsyncFS) Stat(path string) (os.FileInfo, error) {
	if filepath.Base(path) == ".git" && !strings.HasPrefix(path, f.input.Home+string(os.PathSeparator)) {
		return nil, os.ErrNotExist
	}
	return f.OSFileSystem.Stat(path)
}
func (f fixtureAsyncFS) MkdirAll(path string) error {
	err := f.OSFileSystem.MkdirAll(path)
	if err == nil {
		<-f.gate
		*f.events = append(*f.events, Event{"kind": "asyncMkdir", "path": short(f.input.Home, path)})
	}
	return err
}
func (f fixtureAsyncFS) WriteExclusive(path string, data []byte) error {
	if f.input.Fault == "changelog" {
		return fmt.Errorf("private fixture changelog fault")
	}
	return f.OSFileSystem.WriteExclusive(path, data)
}
func short(home, path string) string {
	value, _ := filepath.Rel(home, path)
	return filepath.ToSlash(value)
}
func wire(object *protocol.JSONObject) any {
	if object == nil {
		return nil
	}
	raw, _ := object.MarshalJSON()
	return string(protocol.StringifyValidatedJavaScriptJSON(raw))
}
func executeFixture(input fixtureInput) (map[string]any, error) {
	home := input.Home
	project := filepath.Join(home, "project")
	configHome := filepath.Join(home, ".claude")
	globalPath := filepath.Join(configHome, ".claude.json")
	userPath := filepath.Join(configHome, "settings.json")
	localPath := filepath.Join(project, ".claude", "settings.local.json")
	cachePath := filepath.Join(configHome, "cache", "changelog.md")
	_ = os.MkdirAll(filepath.Dir(localPath), 0777)
	_ = os.MkdirAll(configHome, 0777)
	if input.Global == nil {
		value := "{}"
		input.Global = &value
	}
	for path, raw := range map[string]*string{globalPath: input.Global, userPath: input.User, localPath: input.Local, cachePath: input.Changelog} {
		if raw != nil {
			_ = os.MkdirAll(filepath.Dir(path), 0777)
			if err := os.WriteFile(path, []byte(strings.ReplaceAll(*raw, "<HOME>", home)), 0640); err != nil {
				return nil, err
			}
		}
	}
	events := []Event{}
	now := float64(946684800000)
	faultWrites := 0
	global, err := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: &configHome, Now: func() time.Time { return time.UnixMilli(int64(now)) }, PID: 12345, FS: fixtureFS{input: input, events: &events, faultWrites: &faultWrites}, Watcher: func(string, time.Duration, func(cliglobalconfig.FileStat)) func() { return func() {} }, Stderr: func(string) {}})
	if err != nil {
		return nil, err
	}
	defer global.Close()
	if err = global.Enable(); err != nil {
		return nil, err
	}
	state, err := clisettings.NewState(func() float64 { return now })
	if err != nil {
		return nil, err
	}
	settings, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: configHome, OriginalCWD: project, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return now }, PID: 12345, FS: fixtureSettingsFS{input: input}, Diagnostic: func(raw json.RawMessage) { events = append(events, Event{"kind": "error", "message": raw}) }, Observe: func(effect clisettings.Effect) {
		if effect.Kind == "writeFlush" {
			events = append(events, Event{"kind": "write", "path": short(home, effect.Path), "rawBase64": base64.StdEncoding.EncodeToString([]byte(effect.Bytes))})
		}
	}})
	if err != nil {
		return nil, err
	}
	if input.Schema != "" {
		parsed, err := clisettings.ValidateJSONContent(input.Schema, userPath)
		if err != nil {
			return nil, err
		}
		return map[string]any{"success": parsed.Settings != nil, "settings": wire(parsed.Settings), "errors": parsed.Errors}, nil
	}
	gate := make(chan struct{})
	var override json.RawMessage
	if input.Override != nil {
		override, _ = json.Marshal(*input.Override)
	}
	service, err := New(Options{Global: global, Settings: settings, Host: Host{Profile: ExternalEmptyOAuthProfile, OriginalCWD: project, EmptyCredentialStore: true, Environment: input.Env, ModelOverride: override}, ConfigHome: configHome, Now: func() float64 { return now }, FS: fixtureAsyncFS{input: input, events: &events, gate: gate}, Observe: func(event Event) {
		if event["kind"] == "asyncWrite" {
			event["path"] = short(home, event["path"].(string))
		}
		if event["kind"] == "updaterGlobal" || event["kind"] == "updaterProject" {
			_, err := os.Stat(globalPath + ".lock")
			event["lockExists"] = err == nil
		}
		events = append(events, event)
	}})
	if err != nil {
		return nil, err
	}
	files := func() map[string]any {
		result := map[string]any{}
		paths := []string{globalPath, userPath, localPath, cachePath}
		backupDir := filepath.Join(configHome, "backups")
		if entries, err := os.ReadDir(backupDir); err == nil {
			for _, entry := range entries {
				paths = append(paths, filepath.Join(backupDir, entry.Name()))
			}
		}
		for _, path := range paths {
			info, err := os.Stat(path)
			if err != nil {
				result[short(home, path)] = nil
			} else {
				data, _ := os.ReadFile(path)
				result[short(home, path)] = map[string]any{"rawBase64": base64.StdEncoding.EncodeToString(data), "mode": info.Mode().Perm()}
			}
		}
		return result
	}
	snapshot := func() map[string]any {
		value, _ := global.Get()
		var model any
		if raw := service.ModelOverride(); raw != nil {
			_ = json.Unmarshal(raw, &model)
		}
		_, err := os.Stat(globalPath + ".lock")
		return map[string]any{"global": wire(value), "override": model, "disableAutoUpdater": service.DisableAutoUpdater(), "files": files(), "lockExists": err == nil}
	}
	events = nil
	task, runErr := service.Run()
	sync := snapshot()
	close(gate)
	_ = task.Wait()
	complete := snapshot()
	if input.Twice {
		task, runErr = service.Run()
		_ = task.Wait()
	}
	var message any
	if runErr != nil {
		message = "Error: " + runErr.Error()
	}
	return map[string]any{"error": message, "sync": sync, "complete": complete, "final": snapshot(), "effects": events}, nil
}
func TestCLIMigrationsFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_CLI_MIGRATIONS_FIXTURE") != "1" {
		t.Skip("private fixture entry")
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	var input fixtureInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	if input.Home == "" || input.Home != os.Getenv("HOME") || filepath.Join(input.Home, ".claude") != os.Getenv("CLAUDE_CONFIG_DIR") || !filepath.IsAbs(input.Home) || !strings.HasPrefix(input.Home, filepath.Clean(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))+string(os.PathSeparator)) {
		t.Fatal("migrations require disposable HOME/config")
	}
	result, err := executeFixture(input)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(map[string]any{"name": input.Name, "result": result})
	fmt.Printf("%s\n", out)
}
