package cliglobalconfig

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type fixtureStep struct {
	Operation, Updater, Patch, Raw, Key string
	Milliseconds                        int64
}
type fixtureInput struct {
	Name, Home, Raw, Suffix, Lock string
	RawBytes                      []byte
	Legacy, Symlink               bool
	Mode                          uint32
	Backups                       map[string]string
	BackupBytes                   map[string][]byte
	Steps                         []fixtureStep
	HasRaw                        bool
	Faults                        []fixtureFault
}
type fixtureFault struct {
	Operation, Match string
	Occurrence       int
	Repeat           bool
}
type fixtureResult struct {
	Operation string         `json:"operation"`
	Value     *string        `json:"value"`
	Calls     int            `json:"calls"`
	Error     any            `json:"error"`
	Files     map[string]any `json:"files"`
}
type fixtureOutput struct {
	Path    string           `json:"path"`
	Results []fixtureResult  `json:"results"`
	Effects []map[string]any `json:"effects"`
}

type fixtureTimer struct{ ready chan time.Time }

func (timer *fixtureTimer) Channel() <-chan time.Time { return timer.ready }
func (timer *fixtureTimer) Stop() bool                { return true }

type fixtureReadDelay struct {
	hold                   atomic.Bool
	started, release, done chan struct{}
}

func fixtureGuard(input fixtureInput) error {
	homeStat, err := os.Lstat(input.Home)
	if err != nil || homeStat.Mode()&os.ModeSymlink != 0 || !homeStat.IsDir() {
		return fmt.Errorf("disposable global config fixture HOME/config required")
	}
	configStat, configErr := os.Lstat(filepath.Join(input.Home, ".claude"))
	if configErr != nil && !os.IsNotExist(configErr) || configErr == nil && (configStat.Mode()&os.ModeSymlink != 0 || !configStat.IsDir()) {
		return fmt.Errorf("disposable global config fixture HOME/config required")
	}
	home, err := filepath.EvalSymlinks(input.Home)
	if err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	cwd, _ = filepath.EvalSymlinks(cwd)
	if os.Getenv("CC_HAHA_GLOBAL_CONFIG_FIXTURE") != "1" || os.Getenv("NODE_ENV") != "production" || os.Getenv("BUN_OPTIONS") != "--no-env-file" || !filepath.IsAbs(input.Home) || !strings.HasPrefix(home, root+string(filepath.Separator)) || cwd != home || input.Home != os.Getenv("HOME") || input.Home != os.Getenv("USERPROFILE") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") {
		return fmt.Errorf("disposable global config fixture HOME/config required")
	}
	return nil
}
func fixtureString(object *protocol.JSONObject) string {
	raw, err := object.MarshalJSON()
	if err != nil {
		panic(err)
	}
	raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

type fixtureFS struct {
	OSFileSystem
	home, path, backup string
	effects            *[]map[string]any
	faults             []fixtureFault
	counts             map[int]int
	readDelay          *fixtureReadDelay
}

func (filesystem fixtureFS) fault(operation string, paths ...string) error {
	for index, rule := range filesystem.faults {
		if rule.Operation != operation {
			continue
		}
		matched := rule.Match == ""
		for _, path := range paths {
			matched = matched || strings.Contains(path, rule.Match)
		}
		if !matched {
			continue
		}
		filesystem.counts[index]++
		count := filesystem.counts[index]
		if count == rule.Occurrence || rule.Repeat && count > rule.Occurrence {
			return fmt.Errorf("fixture fault: %s", operation)
		}
	}
	return nil
}
func (filesystem fixtureFS) ReadFile(path string) ([]byte, error) {
	if filesystem.relevant(path) {
		if err := filesystem.fault("read", path); err != nil {
			return nil, err
		}
	}
	raw, err := filesystem.OSFileSystem.ReadFile(path)
	if filesystem.readDelay != nil && filesystem.readDelay.hold.Swap(false) {
		close(filesystem.readDelay.started)
		<-filesystem.readDelay.release
	}
	return raw, err
}
func (filesystem fixtureFS) Stat(path string) (FileStat, error) {
	if filesystem.relevant(path) {
		if err := filesystem.fault("stat", path); err != nil {
			return FileStat{}, err
		}
	}
	return filesystem.OSFileSystem.Stat(path)
}
func (filesystem fixtureFS) Readlink(path string) (string, error) {
	if err := filesystem.fault("readlink", path); err != nil {
		return "", err
	}
	return filesystem.OSFileSystem.Readlink(path)
}
func (filesystem fixtureFS) Chmod(path string, mode fs.FileMode) error {
	if err := filesystem.fault("chmod", path); err != nil {
		return err
	}
	return filesystem.OSFileSystem.Chmod(path, mode)
}

func (filesystem fixtureFS) relevant(path string) bool {
	return path == filesystem.path || path == filesystem.path+".lock" || strings.HasPrefix(path, filesystem.backup) || strings.HasPrefix(path, filesystem.path+".tmp.") || strings.HasPrefix(path, filepath.Join(filepath.Dir(filesystem.path), "actual-config.json"))
}
func (filesystem fixtureFS) short(path string) string {
	value, _ := filepath.Rel(filesystem.home, path)
	return filepath.ToSlash(value)
}
func (filesystem fixtureFS) mutate(kind string, paths ...string) {
	if filesystem.relevant(paths[0]) {
		short := make([]string, len(paths))
		for index, path := range paths {
			short[index] = filesystem.short(path)
		}
		*filesystem.effects = append(*filesystem.effects, map[string]any{"kind": kind, "paths": short})
	}
}
func (filesystem fixtureFS) MkdirAll(path string) error {
	if err := filesystem.fault("mkdir", path); err != nil {
		return err
	}
	err := filesystem.OSFileSystem.MkdirAll(path)
	if err == nil {
		filesystem.mutate("mkdirSync", path)
	}
	return err
}
func (filesystem fixtureFS) CopyFile(source, target string) error {
	if err := filesystem.fault("copy", source, target); err != nil {
		return err
	}
	err := filesystem.OSFileSystem.CopyFile(source, target)
	if err == nil {
		filesystem.mutate("copyFileSync", source, target)
	}
	return err
}
func (filesystem fixtureFS) Rename(source, target string) error {
	if err := filesystem.fault("rename", source, target); err != nil {
		return err
	}
	err := filesystem.OSFileSystem.Rename(source, target)
	if err == nil {
		filesystem.mutate("renameSync", source, target)
	}
	return err
}
func (filesystem fixtureFS) Remove(path string) error {
	if path != filesystem.path+".lock" {
		if err := filesystem.fault("remove", path); err != nil {
			return err
		}
	}
	err := filesystem.OSFileSystem.Remove(path)
	if err == nil && path != filesystem.path+".lock" {
		filesystem.mutate("unlinkSync", path)
	}
	return err
}
func (filesystem fixtureFS) WriteFlush(path string, raw []byte, mode *fs.FileMode) error {
	if filesystem.relevant(path) {
		var permissions any
		if mode != nil {
			permissions = uint32(*mode)
		}
		*filesystem.effects = append(*filesystem.effects, map[string]any{"kind": "writeFlush", "path": filesystem.short(path), "bytes": string(raw), "mode": permissions})
	}
	if err := filesystem.fault("write", path); err != nil {
		return err
	}
	err := filesystem.OSFileSystem.WriteFlush(path, raw, mode)
	return err
}
func TestFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_GLOBAL_CONFIG_FIXTURE") != "1" {
		t.Skip("explicit private fixture only")
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	var input fixtureInput
	if err = json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	var shape map[string]json.RawMessage
	_ = json.Unmarshal(raw, &shape)
	_, input.HasRaw = shape["raw"]
	if err = fixtureGuard(input); err != nil {
		t.Fatal(err)
	}
	result, stderr, err := runFixture(input)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	fmt.Println(string(encoded))
	fmt.Fprint(os.Stderr, stderr)
}
func runFixture(input fixtureInput) (fixtureOutput, string, error) {
	output := fixtureOutput{Results: []fixtureResult{}, Effects: []map[string]any{}}
	config := filepath.Join(input.Home, ".claude")
	path := filepath.Join(config, ".claude"+input.Suffix+".json")
	if input.Legacy {
		path = filepath.Join(config, ".config.json")
	}
	now := time.UnixMilli(946684800000)
	if err := os.MkdirAll(config, 0777); err != nil {
		return output, "", err
	}
	if input.HasRaw || input.RawBytes != nil {
		target := path
		if input.Symlink {
			target = filepath.Join(config, "actual-config.json")
		}
		mode := os.FileMode(input.Mode)
		if mode == 0 {
			mode = 0600
		}
		raw := []byte(strings.ReplaceAll(input.Raw, "<HOME>", input.Home))
		if input.RawBytes != nil {
			raw = input.RawBytes
		}
		if err := os.WriteFile(target, raw, mode); err != nil {
			return output, "", err
		}
		_ = os.Chtimes(target, now.Add(-time.Second), now.Add(-time.Second))
		if input.Symlink {
			if err := os.Symlink("actual-config.json", path); err != nil {
				return output, "", err
			}
		}
	}
	backup := filepath.Join(config, "backups")
	for name, raw := range input.Backups {
		_ = os.MkdirAll(backup, 0777)
		if err := os.WriteFile(filepath.Join(backup, name), []byte(raw), 0600); err != nil {
			return output, "", err
		}
	}
	for name, raw := range input.BackupBytes {
		_ = os.MkdirAll(backup, 0777)
		if err := os.WriteFile(filepath.Join(backup, name), raw, 0600); err != nil {
			return output, "", err
		}
	}
	if input.Lock != "" {
		_ = os.Mkdir(path+".lock", 0777)
		stamp := now
		if input.Lock == "stale" {
			stamp = stamp.Add(-11 * time.Second)
		}
		_ = os.Chtimes(path+".lock", stamp, stamp)
	}
	var stderr strings.Builder
	var watcher func(FileStat)
	scheduled := make(chan *fixtureTimer, 10)
	compromised := make(chan struct{}, 10)
	var nextTimer *fixtureTimer
	var releaseLease func() error
	readDelay := &fixtureReadDelay{started: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
	leaseProfile := false
	for _, step := range input.Steps {
		leaseProfile = leaseProfile || strings.HasPrefix(step.Operation, "lease")
	}
	options := Options{Home: input.Home, ConfigDir: &config, OAuthFileSuffix: input.Suffix, Now: func() time.Time { return now }, PID: 12345, FS: fixtureFS{home: input.Home, path: path, backup: backup, effects: &output.Effects, faults: input.Faults, counts: map[int]int{}, readDelay: readDelay}, Watcher: func(_ string, _ time.Duration, callback func(FileStat)) func() { watcher = callback; return func() {} }, Stderr: func(value string) { stderr.WriteString(value) }}
	if leaseProfile {
		options.Timer = func(_ time.Duration) LeaseTimer {
			timer := &fixtureTimer{ready: make(chan time.Time, 1)}
			scheduled <- timer
			return timer
		}
		options.Diagnostic = func(kind string, value map[string]any) {
			if kind == "lock_compromised" {
				output.Effects = append(output.Effects, map[string]any{"kind": "compromised", "message": value["error"]})
				compromised <- struct{}{}
			}
		}
	}
	service, err := New(options)
	if err != nil {
		return output, "", err
	}
	defer service.Close()
	short := func(path string) string { value, _ := filepath.Rel(input.Home, path); return filepath.ToSlash(value) }
	leaseState := func() *protocol.JSONObject {
		state := &protocol.JSONObject{}
		stat, failure := os.Stat(path + ".lock")
		if failure != nil {
			state.Set("exists", json.RawMessage("false"))
			state.Set("mtime", json.RawMessage("null"))
		} else {
			state.Set("exists", json.RawMessage("true"))
			state.Set("mtime", json.RawMessage(fmt.Sprint(stat.ModTime().UnixMilli())))
		}
		return state
	}
	snapshot := func() map[string]any {
		files := map[string]any{}
		_ = filepath.WalkDir(config, func(file string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				if file != config && file != backup && file != path+".lock" {
					return filepath.SkipDir
				}
				return nil
			}
			if file != path && !strings.HasPrefix(file, backup+string(filepath.Separator)) && !strings.HasPrefix(file, path+".tmp.") && file != filepath.Join(config, "actual-config.json") {
				return nil
			}
			stat, _ := os.Lstat(file)
			if stat.Mode()&os.ModeSymlink != 0 {
				target, _ := os.Readlink(file)
				files[short(file)] = map[string]any{"symlink": target}
			} else {
				raw, _ := os.ReadFile(file)
				files[short(file)] = map[string]any{"bytes": string(storage.DecodeUTF8(raw)), "rawBase64": base64.StdEncoding.EncodeToString(raw), "mode": stat.Mode().Perm()}
			}
			return nil
		})
		return files
	}
	for _, step := range input.Steps {
		result := fixtureResult{Operation: step.Operation}
		var value *protocol.JSONObject
		updater := func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			result.Calls++
			output.Effects = append(output.Effects, map[string]any{"kind": "updater", "current": fixtureString(current)})
			if step.Updater == "throw" || step.Updater == "throwOnce" && result.Calls == 1 {
				return nil, fmt.Errorf("fixture updater failure")
			}
			if step.Updater == "same" {
				return current, nil
			}
			copy := current.Clone()
			patch := step.Patch
			if patch == "" {
				patch = "{}"
			}
			var extra protocol.JSONObject
			if err := extra.UnmarshalJSON([]byte(patch)); err != nil {
				return nil, err
			}
			copy.Assign(extra)
			return &copy, nil
		}
		switch step.Operation {
		case "enable":
			err = service.Enable()
		case "get":
			value, err = service.Get()
		case "save":
			err = service.Save(updater)
		case "project":
			value, err = service.GetProject(input.Home)
		case "saveProject":
			err = service.SaveProject(input.Home, updater)
		case "firstStart":
			err = service.RecordFirstStartTime()
		case "advance":
			now = now.Add(time.Duration(step.Milliseconds) * time.Millisecond)
		case "leaseAcquire":
			releaseLease, err = service.acquireLock()
			if err == nil {
				nextTimer = <-scheduled
				value = leaseState()
			}
		case "leaseTick":
			if nextTimer == nil {
				err = fmt.Errorf("no scheduled lease timer")
			} else {
				nextTimer.ready <- now
				select {
				case nextTimer = <-scheduled:
				case <-compromised:
					nextTimer = nil
				case <-time.After(time.Second):
					err = fmt.Errorf("lease timer did not complete")
				}
				value = leaseState()
			}
		case "leaseRelease":
			err = releaseLease()
			if err == nil {
				value = leaseState()
			}
		case "leaseSteal":
			err = os.Chtimes(path+".lock", now.Add(2*time.Second), now.Add(2*time.Second))
		case "leaseRemove":
			err = os.Remove(path + ".lock")
		case "externalWrite":
			err = os.WriteFile(path, []byte(step.Raw), 0600)
			_ = os.Chtimes(path, now.Add(2*time.Second), now.Add(2*time.Second))
		case "remove":
			err = os.Remove(path)
		case "poll":
			if watcher != nil {
				stat, _ := os.Stat(path)
				watcher(FileStat{Mtime: float64(stat.ModTime().UnixNano()) / 1e6, Size: stat.Size(), Mode: stat.Mode()})
			}
		case "pollPending":
			stat, _ := os.Stat(path)
			readDelay.hold.Store(true)
			go func() {
				watcher(FileStat{Mtime: float64(stat.ModTime().UnixNano()) / 1e6, Size: stat.Size(), Mode: stat.Mode()})
				close(readDelay.done)
			}()
			<-readDelay.started
		case "releasePoll":
			close(readDelay.release)
			<-readDelay.done
		}
		if err != nil {
			result.Error = map[string]any{"name": "Error", "message": err.Error()}
			if failure, ok := err.(interface{ ErrorName() string }); ok {
				result.Error = map[string]any{"name": failure.ErrorName(), "message": err.Error()}
			}
			if failure, ok := err.(*ConfigParseError); ok {
				result.Error = map[string]any{"name": "ConfigParseError", "message": failure.MessageJSON, "filePath": short(failure.FilePath), "defaults": fixtureString(failure.DefaultConfig)}
			}
		}
		if value != nil {
			serialized := fixtureString(value)
			result.Value = &serialized
		}
		result.Files = snapshot()
		output.Results = append(output.Results, result)
		err = nil
	}
	output.Path = short(service.Path())
	return output, stderr.String(), nil
}
