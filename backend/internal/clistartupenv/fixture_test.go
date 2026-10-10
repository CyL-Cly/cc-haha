package clistartupenv

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type fixtureOperation struct{ Kind, Raw, File string }
type fixtureInput struct {
	Home, Name                                                          string
	Global, User, Project, Local, Flag, FlagInline, CcHaha, Environment *string
	CcHahaBase64                                                        *string
	FlagSameAsUser                                                      bool
	Allowed                                                             []string
	Operations                                                          []fixtureOperation
}
type fixturePhysicalObserver struct {
	paths          map[string]string
	canonicalPaths map[string]string
	reads          *[]map[string]any
	allReads       *[]map[string]any
	completeEvents *[]Event
	phase          *string
	nativeCalls    *[]map[string]any
	entrances      *[]map[string]any
	live           map[uintptr]map[string]any
}

func (f *fixturePhysicalObserver) read(path string, run func() ([]byte, error)) ([]byte, error) {
	if f == nil {
		return run()
	}
	logical, canonical := []string{}, []string{}
	for _, key := range []string{"global", "user", "project", "local", "flag", "ccHaha", "providers"} {
		if f.paths[key] == path {
			logical = append(logical, key)
		}
		if f.canonicalPaths[key] == path {
			canonical = append(canonical, key)
		}
	}
	identity := append([]string{}, logical...)
	for _, key := range canonical {
		found := false
		for _, v := range identity {
			if v == key {
				found = true
			}
		}
		if !found {
			identity = append(identity, key)
		}
	}
	if f.completeEvents != nil && len(identity) > 0 {
		*f.completeEvents = append(*f.completeEvents, Event{"kind": "read", "path": f.paths[identity[0]], "identityKeys": identity})
	}
	row := map[string]any{"path": path, "logicalKeys": logical, "canonicalKeys": canonical, "bytesBase64": nil, "error": nil}
	*f.reads = append(*f.reads, row)
	if f.allReads != nil {
		nativeRow := map[string]any{"ordinal": len(*f.allReads) + 1, "phase": *f.phase, "path": path, "logicalKeys": logical, "canonicalKeys": canonical, "identityKeys": identity, "nativeDelegate": true, "encoding": nil, "resultKind": nil, "bytesBase64": nil, "error": nil}
		*f.allReads = append(*f.allReads, nativeRow)
		defer func() {
			nativeRow["bytesBase64"] = row["bytesBase64"]
			nativeRow["error"] = row["error"]
			if row["error"] == nil {
				nativeRow["resultKind"] = "buffer"
			}
		}()
	}
	var nativeRow map[string]any
	if f.nativeCalls != nil {
		nativeRow = f.start("ReadFile", map[string]any{"path": path}, nil)
	}
	raw, err := run()
	if nativeRow != nil {
		nativeRow["result"] = map[string]any{"nativeType": "[]uint8", "nil": raw == nil, "length": len(raw), "bytesBase64": base64.StdEncoding.EncodeToString(raw)}
		nativeRow["error"] = fixtureNativeError(err)
		nativeRow["sameErrorReturned"] = true
		nativeRow["originalResultReturned"] = true
		nativeRow["completed"] = true
	}
	if err != nil {
		code := ""
		if os.IsNotExist(err) {
			code = "ENOENT"
		}
		row["error"] = map[string]any{"name": "PathError", "code": code, "message": err.Error()}
	} else {
		row["bytesBase64"] = base64.StdEncoding.EncodeToString(raw)
	}
	return raw, err
}

type fixtureGlobalFS struct {
	cliglobalconfig.OSFileSystem
	events   *[]Event
	paths    map[string]bool
	observer *fixturePhysicalObserver
}

func (f fixtureGlobalFS) ReadFile(path string) ([]byte, error) {
	if f.paths[path] {
		*f.events = append(*f.events, Event{"kind": "read", "path": path})
	}
	return f.observer.read(path, func() ([]byte, error) { return f.OSFileSystem.ReadFile(path) })
}

type fixtureSettingsFS struct {
	clisettings.OSFileSystem
	events   *[]Event
	paths    map[string]bool
	observer *fixturePhysicalObserver
	home     string
}

func (f fixtureSettingsFS) ContentReadStarted(path string) {
	if f.paths[path] {
		*f.events = append(*f.events, Event{"kind": "read", "path": path})
	}
	if f.observer != nil {
		f.observer.entrance(path)
	}
}
func (f fixtureSettingsFS) ReadFile(path string) ([]byte, error) {
	return f.observer.read(path, func() ([]byte, error) { return f.OSFileSystem.ReadFile(path) })
}
func (f fixtureSettingsFS) OpenReadFile(path string) (clisettings.ReadHandle, error) {
	if f.observer == nil || f.observer.nativeCalls == nil {
		return f.OSFileSystem.OpenReadFile(path)
	}
	row := f.observer.start("OpenReadFile", map[string]any{"path": path}, nil)
	handle, err := f.OSFileSystem.OpenReadFile(path)
	row["result"] = map[string]any{"nativeType": fmt.Sprintf("%T", handle), "interfaceNil": handle == nil}
	row["error"] = fixtureNativeError(err)
	row["sameErrorReturned"] = true
	row["completed"] = true
	if err != nil {
		return handle, err
	}
	return &fixtureNativeHandle{original: handle, observer: f.observer, openRow: row, path: path}, nil
}
func (f fixtureSettingsFS) Stat(path string) (os.FileInfo, error) {
	if filepath.Base(path) == ".git" && !strings.HasPrefix(path, f.home+string(os.PathSeparator)) {
		return nil, os.ErrNotExist
	}
	return f.OSFileSystem.Stat(path)
}

type fixtureFS struct {
	events   *[]Event
	observer *fixturePhysicalObserver
}

func (f fixtureFS) ReadFile(path string) ([]byte, error) {
	*f.events = append(*f.events, Event{"kind": "read", "path": path})
	return f.observer.read(path, func() ([]byte, error) { return os.ReadFile(path) })
}
func parseObject(raw string) protocol.JSONObject {
	var object protocol.JSONObject
	_ = object.UnmarshalJSON([]byte(raw))
	return object
}
func executeFixture(input fixtureInput) (map[string]any, error) {
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
		// Probe inputs belong to the fixture, even when the ambient variable
		// is an empty string. The Source corpus uses a sanitized child env.
		// This explicit Source fixture host control starts as "1"; some
		// cases then mutate it through their original operation sequence.
		if ok && (!seen[string(quote(key))] || key == "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH") {
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
	physicalReads := []map[string]any{}
	allPhysicalReads := []map[string]any{}
	completeEvents := []Event{}
	phase := "init"
	physicalHome, resolveErr := filepath.EvalSymlinks(home)
	if resolveErr != nil {
		return nil, resolveErr
	}
	receiptPaths, canonicalPaths := map[string]string{}, map[string]string{}
	for key, path := range paths {
		receiptPaths[key] = path
		canonicalPaths[key] = strings.Replace(path, home, physicalHome, 1)
	}
	receiptPaths["providers"] = filepath.Join(config, "cc-haha", "providers.json")
	canonicalPaths["providers"] = strings.Replace(receiptPaths["providers"], home, physicalHome, 1)
	settingsReadEntrances := []map[string]any{}
	nativeCalls := []map[string]any{}
	observer := &fixturePhysicalObserver{entrances: &settingsReadEntrances, paths: receiptPaths, canonicalPaths: canonicalPaths, reads: &physicalReads, allReads: &allPhysicalReads, completeEvents: &completeEvents, phase: &phase}
	if os.Getenv("CC_HAHA_STARTUP_NATIVE_RECEIPT") == "1" {
		observer.nativeCalls = &nativeCalls
		observer.live = map[uintptr]map[string]any{}
	}
	observedPaths := map[string]bool{}
	for _, path := range paths {
		observedPaths[path] = true
	}
	global, err := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: &config, Now: func() time.Time { return time.UnixMilli(946684800000) }, PID: 12345, FS: fixtureGlobalFS{events: &events, paths: observedPaths, observer: observer}, Watcher: func(string, time.Duration, func(cliglobalconfig.FileStat)) func() { return func() {} }, Stderr: func(string) {}})
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
	settings, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: project, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 946684800000 }, PID: 12345, FS: fixtureSettingsFS{events: &events, paths: observedPaths, home: home, observer: observer}})
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
	service, err := New(Options{Global: global, Settings: settings, SettingsState: state, Environment: environment, ConfigHome: config, Host: host, FS: fixtureFS{events: &events, observer: observer}, Observe: func(event Event) { events = append(events, event); completeEvents = append(completeEvents, event) }})
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
	for ordinal, operation := range input.Operations {
		phase = fmt.Sprintf("operation:%d:%s", ordinal, operation.Kind)
		completeEvents = []Event{}
		events = []Event{}
		physicalReads = []map[string]any{}
		settingsReadEntrances = []map[string]any{}
		var runErr error
		switch operation.Kind {
		case "safe":
			runErr = service.ApplySafeConfigEnvironmentVariables()
		case "trusted":
			runErr = service.ApplyConfigEnvironmentVariables()
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
		if parsed := state.GetSessionSettingsCache(); parsed != nil {
			raw, _ := parsed.Settings.MarshalJSON()
			sessionSettings = string(protocol.StringifyValidatedJavaScriptJSON(raw))
			sessionView = objectTreeView(*parsed.Settings)
			sessionErrors = parsed.Errors
		}
		row := map[string]any{"operation": operation.Kind, "error": failure, "effects": events, "environment": snapshot(), "sessionSettings": sessionSettings, "sessionView": sessionView, "sessionErrors": sessionErrors}
		if os.Getenv("CC_HAHA_SETTINGS_ALIAS_RECEIPT") == "1" {
			row["physicalReads"] = physicalReads
			row["completeEffects"] = completeEvents
			row["settingsReadEntrances"] = settingsReadEntrances
		}
		results = append(results, row)
	}
	result := map[string]any{"name": input.Name, "results": results}
	if os.Getenv("CC_HAHA_SETTINGS_ALIAS_RECEIPT") == "1" {
		result["declaredPaths"] = receiptPaths
		result["allPhysicalReads"] = allPhysicalReads
		result["canonicalHomeForReceipt"] = physicalHome
		if observer.nativeCalls != nil {
			result["allGoNativeOperations"] = nativeCalls
			result["liveGoDescriptors"] = observer.live
			result["nativeScope"] = "Go adapter API calls, retains raw types/EOF/errors; no policy probes"
		}
	}
	return result, nil
}
func TestCLIStartupEnvFixture(t *testing.T) {
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
	result, err := executeFixture(input)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(result)
	fmt.Printf("%s\n", out)
}

func treeView(raw json.RawMessage) map[string]any {
	if rawKind(raw) == "array" {
		var values []json.RawMessage
		_ = json.Unmarshal(raw, &values)
		items := []map[string]any{}
		for _, value := range values {
			items = append(items, treeView(value))
		}
		return map[string]any{"kind": "array", "items": items}
	}
	if rawKind(raw) == "object" {
		var object protocol.JSONObject
		_ = object.UnmarshalJSON(raw)
		fields := []map[string]any{}
		for _, property := range object.OwnProperties() {
			fields = append(fields, map[string]any{"key": string(property.NameJSON), "value": treeView(property.Value)})
		}
		return map[string]any{"kind": "object", "fields": fields}
	}
	return describe(raw)
}

func objectTreeView(object protocol.JSONObject) map[string]any {
	fields := []map[string]any{}
	for _, property := range object.OwnProperties() {
		fields = append(fields, map[string]any{"key": string(property.NameJSON), "value": treeView(property.Value)})
	}
	return map[string]any{"kind": "object", "fields": fields}
}

// Fixture-only admission resolves the declared disposable allocation root and
// HOME; this permits the actual /var alias without granting another directory.
func fixtureAliasHomeAllowed(home, envHome, config, allocationRoot string) bool {
	if !filepath.IsAbs(home) || home != envHome || filepath.Join(home, ".claude") != config || !filepath.IsAbs(allocationRoot) {
		return false
	}
	info, err := os.Lstat(home)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return false
	}
	canonicalRoot, err := filepath.EvalSymlinks(allocationRoot)
	if err != nil {
		return false
	}
	return strings.HasPrefix(canonicalHome, canonicalRoot+string(os.PathSeparator))
}

func (f *fixturePhysicalObserver) entrance(path string) {
	if f.entrances == nil {
		return
	}
	logical, canonical := []string{}, []string{}
	for _, key := range []string{"global", "user", "project", "local", "flag", "ccHaha", "providers"} {
		if f.paths[key] == path {
			logical = append(logical, key)
		}
		if f.canonicalPaths[key] == path {
			canonical = append(canonical, key)
		}
	}
	*f.entrances = append(*f.entrances, map[string]any{"path": path, "logicalKeys": logical, "canonicalKeys": canonical})
}
func fixtureNativeError(err error) any {
	if err == nil {
		return nil
	}
	row := map[string]any{"nativeType": fmt.Sprintf("%T", err), "message": err.Error(), "isEOF": err == io.EOF, "isNotExist": os.IsNotExist(err)}
	var pe *os.PathError
	if errors.As(err, &pe) {
		row["op"] = pe.Op
		row["path"] = pe.Path
		row["causeNativeType"] = fmt.Sprintf("%T", pe.Err)
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		row["errno"] = uint64(errno)
		row["nativeErrnoString"] = errno.Error()
	}
	return row
}
func (f *fixturePhysicalObserver) start(operation string, args any, origin any) map[string]any {
	row := map[string]any{"ordinal": len(*f.nativeCalls) + 1, "phase": *f.phase, "operation": operation, "args": args, "descriptorOrigin": origin, "result": nil, "error": nil, "completed": false}
	*f.nativeCalls = append(*f.nativeCalls, row)
	return row
}

type fixtureNativeHandle struct {
	original   clisettings.ReadHandle
	observer   *fixturePhysicalObserver
	openRow    map[string]any
	path       string
	fd         uintptr
	fdObserved bool
}

func (f *fixtureNativeHandle) Fd() uintptr {
	row := f.observer.start("Fd", map[string]any{"originalHandleNativeType": reflect.TypeOf(f.original).String()}, nil)
	fd := f.original.Fd()
	f.fd = fd
	f.fdObserved = true
	origin := map[string]any{"path": f.path, "openOrdinal": f.openRow["ordinal"]}
	row["descriptorOrigin"] = origin
	row["result"] = map[string]any{"nativeType": "uintptr", "value": uint64(fd)}
	row["completed"] = true
	f.openRow["descriptorObtainedByOriginalCaller"] = uint64(fd)
	f.observer.live[fd] = origin
	return fd
}
func (f *fixtureNativeHandle) ReadAt(buffer []byte, offset int64) (int, error) {
	origin := map[string]any{"path": f.path, "openOrdinal": f.openRow["ordinal"]}
	row := f.observer.start("ReadAt", map[string]any{"fd": uint64(f.fd), "fdPreviouslyObserved": f.fdObserved, "bufferNativeType": "[]uint8", "bufferBeforeBase64": base64.StdEncoding.EncodeToString(buffer), "bufferLength": len(buffer), "bufferCapacity": cap(buffer), "offsetNativeType": "int64", "offset": offset}, origin)
	n, err := f.original.ReadAt(buffer, offset)
	row["bufferAfterBase64"] = base64.StdEncoding.EncodeToString(buffer)
	row["result"] = map[string]any{"nativeType": "int", "n": n}
	row["error"] = fixtureNativeError(err)
	row["sameErrorReturned"] = true
	row["originalResultReturned"] = true
	row["completed"] = true
	return n, err
}
func (f *fixtureNativeHandle) Close() error {
	origin := map[string]any{"path": f.path, "openOrdinal": f.openRow["ordinal"]}
	row := f.observer.start("Close", map[string]any{"fd": uint64(f.fd), "fdPreviouslyObserved": f.fdObserved}, origin)
	err := f.original.Close()
	row["error"] = fixtureNativeError(err)
	row["result"] = map[string]any{"nativeType": "error", "nil": err == nil}
	row["sameErrorReturned"] = true
	row["completed"] = true
	if err == nil {
		delete(f.observer.live, f.fd)
	}
	return err
}
func fixtureNativeHomeAllowed(home, envHome, config, root string) bool {
	if !filepath.IsAbs(home) || home != envHome || filepath.Join(home, ".claude") != config || !filepath.IsAbs(root) {
		return false
	}
	// This is the frozen alias primitive, not an extra file read/stat observation.
	logicalRoot := strings.TrimPrefix(filepath.Clean(root), "/private")
	logicalHome := strings.TrimPrefix(filepath.Clean(home), "/private")
	return strings.HasPrefix(logicalHome, logicalRoot+string(os.PathSeparator)) && strings.HasPrefix(logicalRoot, "/var/")
}
func TestCLIStartupEnvNativeFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_CLI_STARTUP_ENV_FIXTURE") != "1" || os.Getenv("CC_HAHA_STARTUP_NATIVE_RECEIPT") != "1" || os.Getenv("CC_HAHA_SETTINGS_ALIAS_RECEIPT") != "1" {
		t.Skip("private native fixture entry")
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
	if !fixtureNativeHomeAllowed(input.Home, os.Getenv("HOME"), os.Getenv("CLAUDE_CONFIG_DIR"), os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")) {
		t.Fatal("startup environment requires disposable HOME/config")
	}
	result, err := executeFixture(input)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("%s\n", out)
}
