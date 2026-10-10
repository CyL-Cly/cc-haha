package computeruse

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func fixtureOptions(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	config := filepath.Join(root, "config")
	work := filepath.Join(root, "work")
	temporary := filepath.Join(root, "tmp")
	for _, directory := range []string{home, config, work, temporary} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return Options{HomeDir: home, ConfigDir: config, WorkDir: work, TempDir: temporary, ProjectRoot: filepath.Join(root, "source"), AppRoots: []string{filepath.Join(home, "Applications")}, Platform: "darwin", Arch: "arm64", Environment: map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": temporary, "PATH": "/usr/bin:/bin", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"}}
}
func fixtureService(t *testing.T, options Options) *Service {
	t.Helper()
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	return service
}
func request(t *testing.T, service *Service, method, action, body string) (int, []byte) {
	t.Helper()
	url := "http://localhost/api/computer-use/" + action
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	response := httptest.NewRecorder()
	service.HandleHTTP(response, req, []string{"api", "computer-use", strings.Split(action, "?")[0]})
	return response.Code, response.Body.Bytes()
}
func jsonValue(t *testing.T, raw []byte) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
func TestConfigUsesOriginalShapeUnknownFieldsAndPrivateAtomicWrite(t *testing.T) {
	service := fixtureService(t, fixtureOptions(t))
	ctx := context.Background()
	config, err := service.Config(ctx)
	if err != nil || string(config["enabled"]) != "false" {
		t.Fatalf("default=%s err=%v", encode(config), err)
	}
	if _, err := os.Stat(filepath.Dir(service.ConfigPath())); !os.IsNotExist(err) {
		t.Fatalf("cold read created config parent: %v", err)
	}
	old := `{"enabled":false,"authorizedApps":[{"bundleId":" app.id ","displayName":" Existing ","future":{"keep":[null,17]}}],"grantFlags":{"clipboardRead":false,"futureGrant":{"keep":true}},"pythonPath":" /custom/python ","future":{"version":17},"schema": "user-owned"}`
	if err = os.MkdirAll(filepath.Dir(service.ConfigPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(service.ConfigPath(), []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	status, body := request(t, service, http.MethodPut, "authorized-apps", `{"enabled":true,"grantFlags":{"systemKeyCombos":false},"pythonPath":"\ufeff  "}`)
	if status != 200 {
		t.Fatalf("PUT=%d %s", status, body)
	}
	raw, err := os.ReadFile(service.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := decodeObject(raw)
	if !ok || string(stored["future"]) != "{\n    \"version\": 17\n  }" && !reflect.DeepEqual(jsonValue(t, stored["future"]), map[string]any{"version": float64(17)}) {
		t.Fatalf("unknown fields lost: %s", raw)
	}
	if string(stored["schema"]) != `"user-owned"` || string(stored["pythonPath"]) != "null" {
		t.Fatalf("stored=%s", raw)
	}
	flags, _ := decodeObject(stored["grantFlags"])
	if string(flags["clipboardRead"]) != "false" || string(flags["systemKeyCombos"]) != "false" || flags["futureGrant"] == nil {
		t.Fatalf("flags=%s", stored["grantFlags"])
	}
	info, _ := os.Stat(service.ConfigPath())
	parent, _ := os.Stat(filepath.Dir(service.ConfigPath()))
	if runtime.GOOS != "windows" && (info.Mode().Perm() != 0600 || parent.Mode().Perm() != 0700) {
		t.Fatalf("file=%o parent=%o", info.Mode().Perm(), parent.Mode().Perm())
	}
	if bytes.HasSuffix(raw, []byte("\n")) {
		t.Fatal("changed Source JSON.stringify newline semantics")
	}
	entries, _ := os.ReadDir(filepath.Dir(service.ConfigPath()))
	if len(entries) != 1 {
		t.Fatalf("temporary residue=%v", entries)
	}
	status, body = request(t, service, http.MethodGet, "authorized-apps", "")
	if status != 200 || strings.Contains(string(body), "user-owned") {
		t.Fatalf("GET leaked unknown top-level fields: %d %s", status, body)
	}
	for _, patch := range []string{`null`, `[]`, `{"enabled":1}`, `{"future":1}`, `{"grantFlags":{"future":false}}`, `{"authorizedApps":[{"bundleId":" ","displayName":"X"}]}`} {
		status, _ = request(t, service, http.MethodPut, "authorized-apps", patch)
		if status != 400 {
			t.Fatalf("patch=%s status=%d", patch, status)
		}
	}
	if after, _ := os.ReadFile(service.ConfigPath()); !bytes.Equal(after, raw) {
		t.Fatal("invalid patch mutated protected config")
	}
}
func TestInvalidExistingConfigFailsClosedWithoutRepairOrQuarantine(t *testing.T) {
	service := fixtureService(t, fixtureOptions(t))
	if err := os.MkdirAll(filepath.Dir(service.ConfigPath()), 0700); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`null`, `{"enabled":"true"}`, `{broken`} {
		if err := os.WriteFile(service.ConfigPath(), []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		status, body := request(t, service, "GET", "authorized-apps", "")
		if status != 500 || !strings.Contains(string(body), "COMPUTER_USE_CONFIG_INVALID") {
			t.Fatalf("GET=%d %s", status, body)
		}
		status, body = request(t, service, "PUT", "authorized-apps", `{"enabled":true}`)
		if status != 409 {
			t.Fatalf("PUT=%d %s", status, body)
		}
		if raw, _ := os.ReadFile(service.ConfigPath()); string(raw) != bad {
			t.Fatal("invalid user config auto repaired")
		}
		if _, err := service.Call(context.Background(), "session", "click", json.RawMessage(`{"app":"Any App"}`)); err == nil {
			t.Fatal("invalid config allowed native call")
		}
	}
}
func TestConcurrentConfigPatchesPreserveIndependentUpdates(t *testing.T) {
	service := fixtureService(t, fixtureOptions(t))
	var group sync.WaitGroup
	for _, patch := range []string{`{"enabled":true}`, `{"grantFlags":{"clipboardRead":false}}`, `{"grantFlags":{"clipboardWrite":false}}`, `{"pythonPath":" /python "}`} {
		group.Add(1)
		go func(patch string) {
			defer group.Done()
			if err := service.UpdateConfig(context.Background(), json.RawMessage(patch)); err != nil {
				t.Error(err)
			}
		}(patch)
	}
	group.Wait()
	config, err := service.Config(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	flags, _ := decodeObject(config["grantFlags"])
	if string(config["enabled"]) != "true" || string(config["pythonPath"]) != `"/python"` || string(flags["clipboardRead"]) != "false" || string(flags["clipboardWrite"]) != "false" {
		t.Fatalf("config=%s", encode(config))
	}
}
func TestMacStatusPermissionCardAndRemovedPerAppAuthorization(t *testing.T) {
	options := fixtureOptions(t)
	helper := filepath.Join(options.HomeDir, helperAppName, filepath.FromSlash(helperInner))
	_ = os.MkdirAll(filepath.Dir(helper), 0700)
	_ = os.WriteFile(helper, []byte("fixture"), 0700)
	options.Environment["CC_HAHA_CU_HELPER_PATH"] = helper
	var calls []string
	options.Run = func(_ context.Context, file string, args []string, _ []*os.File) (CommandResult, error) {
		calls = append(calls, file+" "+strings.Join(args, " "))
		switch filepath.Base(file) {
		case "sw_vers":
			return CommandResult{Stdout: "14.4"}, nil
		case "uname":
			return CommandResult{Stdout: "23.4.0"}, nil
		}
		if file == helper && args[0] == "check_permissions" {
			return CommandResult{Stdout: `{"ok":true,"result":{"accessibility":false,"screenRecording":true}}`}, nil
		}
		if file == helper && args[0] == "request-access" {
			return CommandResult{Stdout: "chatter\n{\"ok\":true,\"result\":{\"accessibility\":true,\"screenRecording\":false}}\nlog"}, nil
		}
		return CommandResult{}, nil
	}
	service := fixtureService(t, options)
	status, raw := request(t, service, "GET", "status", "")
	value := jsonValue(t, raw).(map[string]any)
	if status != 200 || value["engine"] != "macos-native" || value["cuHelper"].(map[string]any)["available"] != true || value["permissions"].(map[string]any)["accessibility"] != false {
		t.Fatalf("status=%d %s", status, raw)
	}
	for _, call := range calls {
		if strings.Contains(call, "python") {
			t.Fatal("mac status touched retired Python")
		}
	}
	status, raw = request(t, service, "POST", "open-permission-card", "")
	if status != 200 || jsonValue(t, raw).(map[string]any)["ok"] != true {
		t.Fatalf("card=%d %s", status, raw)
	}
	status, raw = request(t, service, "POST", "request-access", `{"bundleId":"com.apple.SecurityAgent"}`)
	if status != 410 || !strings.Contains(string(raw), "APP_AUTHORIZATION_REMOVED") {
		t.Fatalf("per-app=%d %s", status, raw)
	}
	status, raw = request(t, service, "POST", "setup", "")
	if status != 200 || jsonValue(t, raw).(map[string]any)["success"] != false {
		t.Fatalf("setup=%d %s", status, raw)
	}
	status, raw = request(t, service, "POST", "open-settings", `{"pane":"Privacy_Accessibility","future":"accepted"}`)
	if status != 200 {
		t.Fatalf("open=%d %s", status, raw)
	}
	count := len(calls)
	status, _ = request(t, service, "POST", "open-settings", `{"pane":"../../arbitrary"}`)
	if status != 400 || len(calls) != count {
		t.Fatal("invalid OS pane spawned a process")
	}
}
func TestMacPermissionProbeFailureIsVisibleAndRecorded(t *testing.T) {
	options := fixtureOptions(t)
	helper := filepath.Join(options.HomeDir, "helper")
	_ = os.WriteFile(helper, []byte("helper"), 0700)
	options.Environment["CC_HAHA_CU_HELPER_PATH"] = helper
	options.Run = func(_ context.Context, file string, args []string, _ []*os.File) (CommandResult, error) {
		if strings.HasSuffix(file, "sw_vers") {
			return CommandResult{Stdout: "26.4"}, nil
		}
		if strings.HasSuffix(file, "uname") {
			return CommandResult{Stdout: "25.4"}, nil
		}
		return CommandResult{Stdout: `{"ok":false,"error":{"code":"unauthorized_client","message":"signed process chain rejected"}}`}, nil
	}
	service := fixtureService(t, options)
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var permissions Permissions
	_ = json.Unmarshal(status["permissions"], &permissions)
	if permissions.Accessibility != nil || permissions.Error == nil || *permissions.Error != "signed process chain rejected" {
		t.Fatalf("permissions=%s", status["permissions"])
	}
	raw, err := os.ReadFile(service.options.Diagnostics.DiagnosticsPath())
	if err != nil || !bytes.Contains(raw, []byte("computer_use_permission_probe_failed")) || !bytes.Contains(raw, []byte("permissions stay unknown")) {
		t.Fatalf("diagnostics=%s err=%v", raw, err)
	}
}
func TestNativePrivateAppsPlistAndIconIO(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("actual macOS plutil/sips primitives")
	}
	options := fixtureOptions(t)
	appsRoot := options.AppRoots[0]
	makeApp := func(relative, id, name string) string {
		app := filepath.Join(appsRoot, relative)
		_ = os.MkdirAll(filepath.Join(app, "Contents", "Resources"), 0700)
		plist := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>` + id + `</string><key>CFBundleDisplayName</key><string>` + name + `</string><key>CFBundleIconFile</key><string>AppIcon.png</string></dict></plist>`
		if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0600); err != nil {
			t.Fatal(err)
		}
		return app
	}
	app := makeApp("Vendor/A.app", "private.app", "Private App")
	makeApp("Vendor/A.app/Contents/Hidden.app", "hidden.child", "Hidden Child")
	makeApp("Vendor/Z.app", "private.app", "Duplicate")
	outside := filepath.Join(options.HomeDir, "Outside.app")
	_ = os.MkdirAll(outside, 0700)
	_ = os.Symlink(outside, filepath.Join(appsRoot, "Escape.app"))
	bitmap := image.NewRGBA(image.Rect(0, 0, 2, 2))
	bitmap.Set(0, 0, color.RGBA{255, 0, 0, 255})
	file, err := os.Create(filepath.Join(app, "Contents", "Resources", "AppIcon.png"))
	if err != nil {
		t.Fatal(err)
	}
	_ = png.Encode(file, bitmap)
	_ = file.Close()
	service := fixtureService(t, options)
	apps, err := service.InstalledApps(context.Background())
	if err != nil || len(apps) != 1 || apps[0].BundleID != "private.app" {
		t.Fatalf("apps=%+v err=%v", apps, err)
	}
	icon := service.AppIcon(context.Background(), "private.app", "72")
	decoded, err := png.Decode(bytes.NewReader(icon))
	if err != nil || decoded.Bounds().Dx() != 72 || decoded.Bounds().Dy() != 72 {
		t.Fatalf("icon bounds err=%v bytes=%d", err, len(icon))
	}
	if service.AppIcon(context.Background(), outside, "72") != nil {
		t.Fatal("caller path was accepted instead of installed bundle id")
	}
	icon[0] = 0
	if cached := service.AppIcon(context.Background(), "private.app", "72"); len(cached) == 0 || cached[0] != 137 {
		t.Fatal("caller mutated cached PNG")
	}
	entries, _ := os.ReadDir(options.TempDir)
	if len(entries) != 0 {
		t.Fatalf("icon temp residue=%v", entries)
	}
}
func TestWindowsStatusAndEnablementDoNotStartAnAgentHelper(t *testing.T) {
	options := fixtureOptions(t)
	calls := 0
	options.Run = func(context.Context, string, []string, []*os.File) (CommandResult, error) {
		calls++
		return CommandResult{}, nil
	}
	options.Platform = "win32"
	service := fixtureService(t, options)
	status, _ := request(t, service, "GET", "status", "")
	if status != 200 {
		t.Fatal(status)
	}
	calls = 0
	if _, err := service.Call(context.Background(), "session", "click", json.RawMessage(`{}`)); err == nil || calls != 0 {
		t.Fatalf("disabled err=%v calls=%d", err, calls)
	}
	if err := service.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":true,"authorizedApps":[]}`)); err != nil {
		t.Fatal(err)
	}
	// An explicitly requested Windows action may bootstrap its missing runtime.
	// A runner that never creates that runtime still cannot report a successful action.
	if _, err := service.Call(context.Background(), "session", "click", json.RawMessage(`{}`)); err == nil {
		t.Fatal("missing Windows runtime reported fake success")
	}
	calls = 0
	for _, flag := range []string{"0", "false", "NO", " off "} {
		options.Platform = "darwin"
		options.Environment["CLAUDE_COMPUTER_USE_ENABLED"] = flag
		controlled := fixtureService(t, options)
		if _, err := controlled.Call(context.Background(), "session", "click", json.RawMessage(`{"app":"any"}`)); err == nil || calls != 0 {
			t.Fatalf("flag=%s err=%v calls=%d", flag, err, calls)
		}
	}
}
