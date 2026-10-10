package clisettings

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
)

type writerProfile struct {
	Home                   string            `json:"home"`
	Name                   string            `json:"name"`
	Raw                    *string           `json:"raw"`
	RawBase64              *string           `json:"rawBase64"`
	Encoding               string            `json:"encoding"`
	Operations             []json.RawMessage `json:"operations"`
	FailWrites             bool              `json:"failWrites"`
	Fault, Source, Symlink string
}
type faultFS struct {
	OSFileSystem
	profile writerProfile
}

func (f faultFS) WriteFlush(path string, data []byte) error {
	if f.profile.FailWrites || f.profile.Fault == "temp" && strings.Contains(path, ".tmp.") {
		return errors.New("private fixture write fault")
	}
	return f.OSFileSystem.WriteFlush(path, data)
}
func (f faultFS) Chmod(p string, m os.FileMode) error {
	if f.profile.Fault == "chmod" {
		return errors.New("private fixture chmod fault")
	}
	return f.OSFileSystem.Chmod(p, m)
}
func (f faultFS) Rename(a, b string) error {
	if f.profile.Fault == "rename" {
		return errors.New("private fixture rename fault")
	}
	return f.OSFileSystem.Rename(a, b)
}
func fixtureView(object *protocol.JSONObject, overrides [][]json.RawMessage) any {
	if object == nil {
		return map[string]any{"source": "null", "fields": []any{map[string]any{"path": []json.RawMessage{}, "kind": "null"}}}
	}
	fields := []any{}
	var visit func(json.RawMessage, []json.RawMessage, *protocol.JSONObject)
	visit = func(raw json.RawMessage, path []json.RawMessage, record *protocol.JSONObject) {
		kind := receivedType(raw)
		if record != nil {
			kind = "object"
		}
		for _, override := range overrides {
			if samePath(path, override) {
				kind = "undefined"
				raw = nil
				record = nil
				break
			}
		}
		item := map[string]any{"path": path, "kind": kind}
		if kind == "number" {
			text, _ := protocol.ExactJavaScriptStringJSON(raw)
			var number string
			_ = json.Unmarshal(text, &number)
			item["number"] = number
			n, _ := strconv.ParseFloat(string(raw), 64)
			item["negativeZero"] = n == 0 && len(raw) > 0 && raw[0] == '-'
		}
		if kind == "object" {
			if record == nil {
				var value protocol.JSONObject
				_ = value.UnmarshalJSON(raw)
				record = &value
			}
			keys := []json.RawMessage{}
			for _, property := range record.OwnProperties() {
				keys = append(keys, property.NameJSON)
			}
			item["keys"] = keys
			fields = append(fields, item)
			for _, property := range record.OwnProperties() {
				visit(property.Value, append(append([]json.RawMessage{}, path...), property.NameJSON), nil)
			}
			return
		}
		if kind == "array" {
			var values []json.RawMessage
			_ = json.Unmarshal(raw, &values)
			keys := []json.RawMessage{}
			for i := range values {
				keys = append(keys, quoted(strconv.Itoa(i)))
			}
			item["keys"] = keys
			fields = append(fields, item)
			for i, value := range values {
				visit(value, append(append([]json.RawMessage{}, path...), quoted(strconv.Itoa(i))), nil)
			}
			return
		}
		fields = append(fields, item)
	}
	visit(nil, []json.RawMessage{}, object)
	clone := object.Clone()
	for _, parts := range overrides {
		_ = setUndefinedPath(&clone, parts)
	}
	raw, _ := clone.MarshalJSON()
	return map[string]any{"source": string(protocol.StringifyValidatedJavaScriptJSON(raw)), "fields": fields}
}
func samePath(a, b []json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}
func observeWriter(profile writerProfile) (any, error) {
	home := profile.Home
	project := filepath.Join(home, "project")
	path := filepath.Join(project, ".claude", "settings.json")
	if profile.Source == "userSettings" {
		path = filepath.Join(home, ".claude", "settings.json")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
		return nil, err
	}
	if profile.Raw != nil {
		raw := []byte(*profile.Raw)
		if profile.Encoding == "utf16le" {
			raw = []byte{255, 254}
			for _, unit := range utf16.Encode([]rune(*profile.Raw)) {
				raw = binary.LittleEndian.AppendUint16(raw, unit)
			}
		}
		if err := os.WriteFile(path, raw, 0640); err != nil {
			return nil, err
		}
	}
	if profile.RawBase64 != nil {
		raw, _ := base64.StdEncoding.DecodeString(*profile.RawBase64)
		if err := os.WriteFile(path, raw, 0640); err != nil {
			return nil, err
		}
	}
	if profile.Symlink != "" {
		target := filepath.Join(filepath.Dir(path), "actual.json")
		if err := os.Rename(path, target); err != nil {
			return nil, err
		}
		link := "actual.json"
		if profile.Symlink == "absolute" {
			link = target
		}
		if err := os.Symlink(link, path); err != nil {
			return nil, err
		}
	}
	effects := []Effect{}
	state, _ := NewState(func() float64 { return 946684800000 })
	service, err := NewService(Options{State: state, ConfigHome: filepath.Join(home, ".claude"), OriginalCWD: project, Profile: ExternalReleaseProfile, PID: 12345, Now: func() float64 { return 946684800000 }, FS: faultFS{profile: profile}, Observe: func(e Effect) {
		relevant := func(p string) bool { return p == path || strings.HasPrefix(p, path+".tmp.") }
		if e.Kind != "mark" && e.Kind != "reset" && !relevant(e.Path) && !relevant(e.Source) {
			return
		}
		if e.Path != "" {
			e.Path, _ = filepath.Rel(home, e.Path)
		}
		if e.Source != "" {
			e.Source, _ = filepath.Rel(home, e.Source)
		}
		if e.Target != "" {
			e.Target, _ = filepath.Rel(home, e.Target)
		}
		effects = append(effects, e)
	}})
	if err != nil {
		return nil, err
	}
	results := []any{}
	var last, previous *ParsedSettings
	var sourceValue *protocol.JSONObject
	lastWasWarm := false
	for _, original := range profile.Operations {
		var operation struct {
			Kind, Raw, Patch string
			Path             []json.RawMessage
			UndefinedPaths   [][]json.RawMessage
		}
		_ = json.Unmarshal(original, &operation)
		var observation any
		switch operation.Kind {
		case "getSource":
			previousSource := sourceValue
			source := profile.Source
			if source == "" {
				source = "projectSettings"
			}
			sourceValue, err = service.GetEditableSource(source, "")
			if err != nil {
				return nil, err
			}
			record := map[string]any{"value": fixtureView(sourceValue, nil)}
			if previousSource != nil {
				record["sameSource"] = previousSource == sourceValue
			}
			observation = record
		case "mutateSource":
			if err = setFixturePath(sourceValue, operation.Path, json.RawMessage(operation.Raw)); err != nil {
				return nil, err
			}
		case "parse":
			previous = last
			last, err = service.ParseFile(path)
			if err != nil {
				return nil, err
			}
			record := map[string]any{"value": fixtureView(last.Settings, nil), "errors": last.ErrorReference()}
			if lastWasWarm {
				record["sameSettings"] = false
				record["sameErrors"] = false
				lastWasWarm = false
			} else if previous != nil {
				record["sameSettings"] = previous.Settings == last.Settings
				record["sameErrors"] = previous.ErrorReference() == last.ErrorReference()
			}
			observation = record
		case "mutateErrors":
			*last.ErrorReference() = append(*last.ErrorReference(), json.RawMessage(operation.Raw))
		case "mutate":
			if err = setFixturePath(last.Settings, operation.Path, json.RawMessage(operation.Raw)); err != nil {
				return nil, err
			}
		case "overwrite":
			if err = os.WriteFile(path, []byte(operation.Raw), 0640); err != nil {
				return nil, err
			}
		case "reset":
			state.ResetSettingsCache()
		case "warmContent", "contentAgain":
			if operation.Kind == "warmContent" {
				lastWasWarm = true
			}
			var value protocol.JSONObject
			if profile.Raw != nil {
				_ = value.UnmarshalJSON(parseJSONContent(*profile.Raw))
			}
			observation = fixtureView(&value, nil)
		case "update":
			var patch protocol.JSONObject
			if err = patch.UnmarshalJSON([]byte(operation.Patch)); err != nil {
				return nil, err
			}
			source := profile.Source
			if source == "" {
				source = "projectSettings"
			}
			result, e := service.UpdateEditableSource(source, project, Patch{Object: patch, UndefinedPaths: operation.UndefinedPaths})
			if e != nil {
				return nil, e
			}
			record := map[string]any{"patch": fixtureView(&patch, operation.UndefinedPaths), "error": nil}
			if result.Error != nil {
				record["error"] = map[string]any{"name": "Error", "message": result.Error.Error()}
			}
			observation = record
		case "consumeMark":
			observation = state.ConsumeInternalWrite(path, 5000)
		default:
			return nil, fmt.Errorf("unknown writer fixture operation %s", operation.Kind)
		}
		var file any
		if raw, e := os.ReadFile(path); e == nil {
			info, _ := os.Stat(path)
			file = map[string]any{"text": string(storage.DecodeUTF8(raw)), "rawBase64": base64.StdEncoding.EncodeToString(raw), "mode": uint32(info.Mode().Perm())}
		}
		results = append(results, map[string]any{"operation": original, "observation": observation, "effects": append([]Effect{}, effects...), "file": file, "cachedParsePresent": state.GetCachedParsedFile(path) != nil})
	}
	return map[string]any{"name": profile.Name, "results": results}, nil
}
func setFixturePath(object *protocol.JSONObject, parts []json.RawMessage, value json.RawMessage) error {
	if len(parts) == 1 {
		return object.SetPropertyJSON(parts[0], value)
	}
	var nested protocol.JSONObject
	if err := nested.UnmarshalJSON(propertyValue(*object, parts[0])); err != nil {
		return err
	}
	if err := setFixturePath(&nested, parts[1:], value); err != nil {
		return err
	}
	raw, _ := nested.MarshalJSON()
	return object.SetPropertyJSON(parts[0], raw)
}
func TestCLISettingsFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_CLI_SETTINGS_FIXTURE") != "1" {
		t.Skip("private fixture only")
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	var input writerProfile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	home, e := filepath.EvalSymlinks(input.Home)
	root, r := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	cwd, _ := os.Getwd()
	cwd, _ = filepath.EvalSymlinks(cwd)
	if e != nil || r != nil || home != cwd || input.Home != os.Getenv("HOME") || input.Home != os.Getenv("USERPROFILE") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") || !strings.HasPrefix(home, root+string(filepath.Separator)) || os.Getenv("NODE_ENV") != "production" {
		t.Fatal("CLI settings requires private HOME/config")
	}
	result, err := observeWriter(input)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(out))
}
