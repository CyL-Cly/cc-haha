package clistartupenv

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

func executeInitialGetterFixture(input fixtureInput) (map[string]any, error) {
	home := input.Home
	project := filepath.Join(home, "project")
	config := filepath.Join(home, ".claude")
	paths := map[string]string{"global": filepath.Join(config, ".claude.json"), "user": filepath.Join(config, "settings.json"), "project": filepath.Join(project, ".claude", "settings.json"), "local": filepath.Join(project, ".claude", "settings.local.json"), "flag": filepath.Join(home, "flag.json"), "ccHaha": filepath.Join(config, "cc-haha", "settings.json")}
	if input.FlagSameAsUser {
		paths["flag"] = paths["user"]
	}
	_ = os.MkdirAll(filepath.Join(project, ".claude"), 0777)
	_ = os.MkdirAll(filepath.Join(config, "cc-haha"), 0777)
	if input.Global == nil {
		value := "{}"
		input.Global = &value
	}
	keys := []json.RawMessage{quote("NODE_EXTRA_CA_CERTS")}
	seen := map[string]bool{string(keys[0]): true}
	addKeys := func(object protocol.JSONObject) {
		for _, property := range object.OwnProperties() {
			if !seen[string(property.NameJSON)] {
				seen[string(property.NameJSON)] = true
				keys = append(keys, property.NameJSON)
			}
		}
	}
	for _, field := range []struct {
		name string
		raw  *string
	}{{"global", input.Global}, {"user", input.User}, {"project", input.Project}, {"local", input.Local}, {"flag", input.Flag}, {"inline", input.FlagInline}, {"ccHaha", input.CcHaha}} {
		if field.raw == nil {
			continue
		}
		raw := strings.ReplaceAll(*field.raw, "<HOME>", home)
		object := parseObject(raw)
		env, _ := record(object.Get("env"))
		addKeys(env)
		if path := paths[field.name]; path != "" && !(field.name == "flag" && input.FlagSameAsUser) {
			if err := os.WriteFile(path, []byte(raw), 0640); err != nil {
				return nil, err
			}
		}
	}
	if input.Environment != nil {
		addKeys(parseObject(*input.Environment))
	}
	if input.CcHahaBase64 != nil {
		raw, err := base64.StdEncoding.DecodeString(*input.CcHahaBase64)
		if err != nil {
			return nil, err
		}
		if err = os.WriteFile(paths["ccHaha"], raw, 0640); err != nil {
			return nil, err
		}
		decoded := parseObject(string(protocol.DecodeJavaScriptUTF8(raw)))
		env, _ := record(decoded.Get("env"))
		addKeys(env)
	}
	for _, operation := range input.Operations {
		object := parseObject(operation.Raw)
		if operation.Kind != "setEnv" {
			object, _ = record(object.Get("env"))
		}
		addKeys(object)
	}
	for _, key := range []string{"ANTHROPIC_DEFAULT_FABLE_MODEL_SUPPORTED_CAPABILITIES", "ANTHROPIC_DEFAULT_HAIKU_MODEL_SUPPORTED_CAPABILITIES", "ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES", "ANTHROPIC_DEFAULT_OPUS_MODEL_SUPPORTED_CAPABILITIES"} {
		if !seen[string(quote(key))] {
			seen[string(quote(key))] = true
			keys = append(keys, quote(key))
		}
	}
	initial := protocol.NewJSONObject()
	for _, pair := range os.Environ() {
		key, value, ok := strings.Cut(pair, "=")
		if ok {
			initial.Set(key, quote(value))
		}
	}
	if input.Environment != nil {
		initial.Assign(parseObject(strings.ReplaceAll(*input.Environment, "<HOME>", home)))
	}
	environment, err := NewEnvironment(initial)
	if err != nil {
		return nil, err
	}
	events := []Event{}
	observedPaths := map[string]bool{}
	for _, path := range paths {
		observedPaths[path] = true
	}
	global, err := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: &config, Now: func() time.Time { return time.UnixMilli(946684800000) }, PID: 12345, FS: fixtureGlobalFS{events: &events, paths: observedPaths}, Watcher: func(string, time.Duration, func(cliglobalconfig.FileStat)) func() { return func() {} }, Stderr: func(string) {}})
	if err != nil {
		return nil, err
	}
	defer global.Close()
	if err = global.Enable(); err != nil {
		return nil, err
	}
	state, err := clisettings.NewState(func() float64 { return 946684800000 })
	if err != nil {
		return nil, err
	}
	settings, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: project, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 946684800000 }, PID: 12345, FS: fixtureSettingsFS{events: &events, paths: observedPaths, home: home}})
	if err != nil {
		return nil, err
	}
	host := Host{Profile: EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true, AllowedSources: input.Allowed}
	if input.Flag != nil || input.FlagSameAsUser {
		value := paths["flag"]
		host.FlagPath = &value
	}
	if input.FlagInline != nil {
		host.FlagInline = []byte(strings.ReplaceAll(*input.FlagInline, "<HOME>", home))
	}
	service, err := New(Options{Global: global, Settings: settings, SettingsState: state, Environment: environment, ConfigHome: config, Host: host, FS: fixtureFS{events: &events}, Observe: func(event Event) { events = append(events, event) }})
	if err != nil {
		return nil, err
	}
	snapshot := func() []map[string]any {
		result := []map[string]any{}
		object := environment.Snapshot()
		for _, key := range keys {
			raw, own := propertyValue(object, key)
			result = append(result, map[string]any{"key": string(key), "own": own, "value": describe(raw)})
		}
		return result
	}
	results := []map[string]any{}
	var previousInitial *protocol.JSONObject
	for _, operation := range input.Operations {
		events = []Event{}
		var runErr error
		var returned any
		var sameInitial any
		switch operation.Kind {
		case "cacheInitialNull":
			state.SetSessionSettingsCache(&clisettings.ParsedSettings{Settings: nil, Errors: []json.RawMessage{}})
		case "getInitial", "mutateInitial":
			var value *protocol.JSONObject
			value, runErr = service.GetInitialSettings()
			if runErr == nil {
				sameInitial = previousInitial == value
				previousInitial = value
				if operation.Kind == "mutateInitial" {
					value.Assign(parseObject(strings.ReplaceAll(operation.Raw, "<HOME>", home)))
				}
				rendered, marshalErr := value.MarshalJSON()
				if marshalErr != nil {
					runErr = marshalErr
				} else {
					returned = string(protocol.StringifyValidatedJavaScriptJSON(rendered))
				}
			}
		case "safe":
			runErr = service.ApplySafeConfigEnvironmentVariables()
		case "extraCA":
			runErr = service.ApplyExtraCACertsFromConfig()
		case "resetSettings":
			state.ResetSettingsCache()
		case "cacheFlagNull":
			state.SetCachedSettingsForSource("flagSettings", nil)
		case "setEnv":
			runErr = environment.Assign(parseObject(strings.ReplaceAll(operation.Raw, "<HOME>", home)))
		case "write":
			runErr = os.WriteFile(paths[operation.File], []byte(strings.ReplaceAll(operation.Raw, "<HOME>", home)), 0640)
		default:
			return nil, fmt.Errorf("undeclared startup operation")
		}
		var failure any
		if runErr != nil {
			failure = map[string]any{"name": "Error", "message": string(quote(runErr.Error()))}
		}
		var sessionSettings any
		var sessionView any
		var sessionErrors any
		if parsed := state.GetSessionSettingsCache(); parsed != nil && parsed.Settings == nil {
			sessionSettings = "null"
			sessionView = treeView([]byte("null"))
			sessionErrors = parsed.Errors
		}
		if parsed := state.GetSessionSettingsCache(); parsed != nil && parsed.Settings != nil {
			raw, _ := parsed.Settings.MarshalJSON()
			sessionSettings = string(protocol.StringifyValidatedJavaScriptJSON(raw))
			sessionView = objectTreeView(*parsed.Settings)
			sessionErrors = parsed.Errors
		}
		results = append(results, map[string]any{"operation": operation.Kind, "returned": returned, "sameInitial": sameInitial, "error": failure, "effects": events, "environment": snapshot(), "sessionSettings": sessionSettings, "sessionView": sessionView, "sessionErrors": sessionErrors})
	}
	return map[string]any{"name": input.Name, "results": results}, nil
}
func TestInitialGetterFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_CLI_STARTUP_ENV_FIXTURE") != "1" {
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
	if !filepath.IsAbs(input.Home) || input.Home != os.Getenv("HOME") || filepath.Join(input.Home, ".claude") != os.Getenv("CLAUDE_CONFIG_DIR") || !strings.HasPrefix(input.Home, filepath.Clean(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))+string(os.PathSeparator)) {
		t.Fatal("startup environment requires disposable HOME/config")
	}
	result, err := executeInitialGetterFixture(input)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(result)
	fmt.Printf("%s\n", out)
}
