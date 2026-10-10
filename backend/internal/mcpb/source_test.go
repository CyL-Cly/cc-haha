package mcpb

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func parse(t *testing.T, raw string) protocol.JSONObject {
	t.Helper()
	var o protocol.JSONObject
	if err := o.UnmarshalJSON([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	return o
}
func baseManifest(t *testing.T) protocol.JSONObject {
	return parse(t, `{"manifest_version":"0.3","name":"native-peer","version":"1.0","description":"test","author":{"name":"Test"},"server":{"type":"node","entry_point":"server.js","mcp_config":{"command":"node","args":["${__dirname}/server.js"]}}}`)
}

func sourceCompare(t *testing.T, home string, cases []map[string]any, observations []any) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	sdk := os.Getenv("CC_HAHA_MCPB_SOURCE_SDK")
	if bun == "" || sdk == "" {
		t.Skip("Source comparison requires CC_HAHA_SOURCE_BUN and the verified reference CC_HAHA_MCPB_SOURCE_SDK")
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	cmd := exec.Command(bun, "run", filepath.Join(root, "scripts/quality-gate/go-parity/native-mcpb.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + os.TempDir(), "CC_HAHA_MCPB_FIXTURE=1", "CC_HAHA_MCPB_SOURCE_SDK=" + sdk}
	data, _ := json.Marshal(map[string]any{"cases": cases})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	actual, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source fixture: %v\n%s", err, stderr.String())
	}
	want, _ := json.Marshal(observations)
	if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(want)) {
		var a, b []json.RawMessage
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(want, &b)
		for i := range a {
			if i >= len(b) || !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(a[i]), protocol.StringifyValidatedJavaScriptJSON(b[i])) {
				t.Fatalf("case %d %v\nSource %s\nGo %s", i, cases[i], a[i], b[i])
			}
		}
		t.Fatal("Source observation count mismatch")
	}
}

type observation struct {
	Value    any   `json:"value"`
	Warnings []any `json:"warnings"`
	Error    any   `json:"error"`
}

func observe(value any, err error, warnings []any) observation {
	if warnings == nil {
		warnings = []any{}
	}
	var message any
	if err != nil {
		message = err.Error()
		if original, ok := err.(interface{ JSONMessage() json.RawMessage }); ok {
			message = original.JSONMessage()
		}
		value = nil
	}
	return observation{value, warnings, message}
}
func fixtureHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return home
}

func TestManifestAndUserConfigurationAgainstOriginalHelpersAndCompatibleSDK(t *testing.T) {
	home := fixtureHome(t)
	cases := []map[string]any{}
	observations := []any{}
	add := func(raw string) {
		cases = append(cases, map[string]any{"action": "manifest", "raw": raw})
		value, err := ParseManifest([]byte(raw))
		var out any = value
		if err != nil {
			out = nil
		}
		observations = append(observations, observe(out, err, nil))
	}
	base := baseManifest(t)
	data, _ := base.MarshalJSON()
	add(string(data))
	add("\ufeff" + string(data))
	add(`null`)
	add(`[]`)
	add(`{}`)
	for _, raw := range []string{"", `{`, `{"name":1,}`, `{"name":"x}`, `true extra`, `🙂`, `[01]`, `{"bad":"\x"}`} {
		add(raw)
	}
	for _, row := range []struct {
		key   string
		value string
	}{{"manifest_version", `"0.2"`}, {"manifest_version", `null`}, {"author", `{"name":false,"email":"x","url":"y","extra":true}`}, {"icons", `[{"src":"x","size":"3X3","theme":""}]`}, {"localization", `{"resources":"x","default_locale":"_"}`}, {"server", `{"type":"other","entry_point":[],"mcp_config":{"command":null,"args":[3],"env":{"x":1},"platform_overrides":{"darwin":{"extra":1}}},"extra":1}`}, {"user_config", `{"x":{"type":"string","title":"x","description":"x","default":["x",1]}}`}, {"_meta", `{"x":{"1":1e400,"__proto__":{"safe":true}}}`}, {"compatibility", `{"platforms":["windows",null],"runtimes":{"node":false,"extra":1}}`}, {"extra", `"unknown"`}} {
		value := base.Clone()
		value.Set(row.key, json.RawMessage(row.value))
		raw, _ := value.MarshalJSON()
		add(string(raw))
	}
	missingVersion := base.Clone()
	missingVersion.Delete("manifest_version")
	raw, _ := missingVersion.MarshalJSON()
	add(string(raw))
	// Full valid schema, special property names, number normalization and all
	// optional declarations participate in the same original strict parser.
	full := base.Clone()
	for key, value := range map[string]string{"dxt_version": `"0.3"`, "$schema": `"arbitrary"`, "display_name": `"native"`, "long_description": `"long"`, "repository": `{"type":"git","url":"https://example.invalid/"}`, "homepage": `"mailto:test@example.invalid"`, "documentation": `"https://example.invalid/docs"`, "support": `"https://example.invalid/help"`, "icon": `"x"`, "icons": `[{"src":"x","size":"01x22","theme":"dark"}]`, "screenshots": `["x"]`, "localization": `{"resources":"${LOCALE}/x","default_locale":"zh-Hans"}`, "tools": `[{"name":"one","description":"d"}]`, "tools_generated": `true`, "prompts": `[{"name":"p","description":"d","arguments":["x"],"text":"t"}]`, "prompts_generated": `false`, "keywords": `["x"]`, "license": `"MIT"`, "privacy_policies": `["https://example.invalid/p"]`, "compatibility": `{"claude_desktop":"1","platforms":["darwin","win32","linux"],"runtimes":{"node":"22","python":"3"}}`, "user_config": `{"token":{"type":"string","title":"Token","description":"t","required":true,"default":"x","multiple":true,"sensitive":true,"min":-0,"max":1e21},"__proto__":{"type":"file","title":"p","description":"p"}}`, "_meta": `{"x":{"0":1e400,"2":9007199254740993,"__proto__":{"safe":true}}}`} {
		full.Set(key, json.RawMessage(value))
	}
	raw, _ = full.MarshalJSON()
	add(string(raw))
	schema := parse(t, `{"s":{"type":"string","title":"Text","required":true},"a":{"type":"string","multiple":true},"n":{"type":"number","min":2,"max":3},"b":{"type":"boolean"},"f":{"type":"file"},"d":{"type":"directory"}}`)
	for _, raw := range []string{`{}`, `{"s":"","a":[],"n":1,"b":null,"f":1,"d":false}`, `{"s":[],"a":["x",false],"n":4,"b":"true","f":"","d":"/x"}`, `{"s":null,"a":4,"n":"4","b":false,"f":{},"d":[]}`, `{"s":"ok","a":[""],"n":2,"b":true,"f":"/f","d":"/d"}`, `{"s":"x","a":[],"n":1e400}`} {
		values := parse(t, raw)
		valid, failures := ValidateUserConfig(values, schema)
		cases = append(cases, map[string]any{"action": "validate", "values": values, "schema": schema})
		observations = append(observations, observe(protocol.NewJSONObject(protocol.JSONField{Name: "valid", Value: encode(valid)}, protocol.JSONField{Name: "errors", Value: encode(failures)}), nil, nil))
	}
	sourceCompare(t, home, cases, observations)
}

func TestConfigCompilerAgainstCompatiblePublishedSDK(t *testing.T) {
	home := fixtureHome(t)
	cases := []map[string]any{}
	observations := []any{}
	add := func(manifest, user, dirs protocol.JSONObject, platform string) {
		warnings := []any{}
		value, err := GenerateConfig(manifest, ConfigOptions{Platform: platform, ExtensionPath: "/private/extension", PathSeparator: "/", SystemDirectories: dirs, UserConfig: user, OnArrayWarning: func(key string, replacement, original json.RawMessage) {
			warnings = append(warnings, map[string]any{"key": key, "replacement": replacement})
		}})
		var out any = value
		cases = append(cases, map[string]any{"action": "config", "manifest": manifest.Clone(), "userConfig": user.Clone(), "systemDirs": dirs.Clone(), "platform": platform, "extensionPath": "/private/extension", "pathSeparator": "/"})
		observations = append(observations, observe(out, err, warnings))
	}
	base := baseManifest(t)
	empty := protocol.NewJSONObject()
	dirs := parse(t, `{"HOME":"/private/home","DESKTOP":"/private/desk"}`)
	add(base, empty, dirs, "darwin")
	add(empty, empty, dirs, "linux")
	server := object(base.Get("server"))
	server.Set("mcp_config", json.RawMessage(`{"command":"${HOME}/${__dirname}","args":["${user_config.multi}","X${user_config.multi}","${user_config.flag}","${user_config.blank}","${user_config.number}","${user_config.missing}","${user_config.}","${/}"],"env":{"DATA":"${DESKTOP}","__proto__":"drop"},"platform_overrides":{"darwin":{"command":"alternate","args":[],"env":{"X":"yes"}},"linux":{"command":"","args":["${user_config.multi}"],"env":{}}}}`))
	base.Set("server", encode(server))
	base.Set("user_config", json.RawMessage(`{"multi":{"default":["default"]},"flag":{"default":false},"number":{"default":0},"blank":{"default":""}}`))
	for _, platform := range []string{"darwin", "linux", "win32"} {
		for _, raw := range []string{`{}`, `{"multi":["a","b"],"flag":true,"number":1e21}`, `{"multi":[],"flag":false,"number":null}`, `{"multi":[null,false,4,{},[1,2]],"blank":"","number":1e400}`} {
			add(base, parse(t, raw), dirs, platform)
		}
	}
	for _, raw := range []string{`{}`, `{"required":null}`, `{"required":""}`, `{"required":[]}`, `{"required":["ok",null]}`, `{"required":false}`, `{"required":0}`, `{"required":"ok"}`} {
		required := base.Clone()
		required.Set("user_config", json.RawMessage(`{"required":{"required":true,"default":"fallback"}}`))
		add(required, parse(t, raw), dirs, "win32")
	}
	for _, pair := range []struct{ pattern, user string }{{"${user_config.x}", `{"x":"$$/$&/$` + "`" + `/$'/literal"}`}, {"${user_config.axb}", `{"a.b":"regex-dot"}`}, {"${user_config.abc}", `{"(abc)":"$1/$2/$10/$01"}`}, {"${user_config.a}", `{"(?<group>a)":"$<group>/$<missing>"}`}, {"${user_config.😊}", `{"😊":"emoji😊"}`}} {
		m := baseManifest(t)
		s := object(m.Get("server"))
		conf := protocol.NewJSONObject(protocol.JSONField{Name: "command", Value: quote("node")}, protocol.JSONField{Name: "args", Value: encode([]string{pair.pattern, "pre" + pair.pattern + "post"})})
		s.Set("mcp_config", encode(conf))
		m.Set("server", encode(s))
		add(m, parse(t, pair.user), dirs, "darwin")
	}
	m := baseManifest(t)
	server = object(m.Get("server"))
	server.Set("mcp_config", json.RawMessage(`{"command":"node","args":["${user_config.\ud800}","pre${user_config.\ud800}post"]}`))
	m.Set("server", encode(server))
	add(m, parse(t, `{"\ud800":"lone\ud801"}`), dirs, "darwin")
	sourceCompare(t, home, cases, observations)
}

func TestRequiredDefaultDoesNotSubstituteForProvidedConfiguration(t *testing.T) {
	m := baseManifest(t)
	m.Set("user_config", json.RawMessage(`{"token":{"type":"string","title":"Token","description":"token","required":true,"default":"unsafe-default"}}`))
	value, err := GenerateConfig(m, ConfigOptions{UserConfig: protocol.NewJSONObject()})
	if err != nil || value != nil {
		t.Fatalf("required user value missing: %v %v", value, err)
	}
	valid, failures := ValidateUserConfig(protocol.NewJSONObject(), object(m.Get("user_config")))
	if valid || !reflect.DeepEqual(failures, []string{"Token is required but not provided"}) {
		t.Fatal(valid, failures)
	}
}

func TestSystemDirectoriesAgainstOriginalSource(t *testing.T) {
	home := fixtureHome(t)
	cases := []map[string]any{}
	observations := []any{}
	for _, platform := range []string{"windows", "linux", "wsl", "macos", "unknown"} {
		for _, env := range []map[string]string{{}, {"USERPROFILE": "/private/profile", "XDG_DESKTOP_DIR": "/private/desktop", "XDG_DOCUMENTS_DIR": "/private/documents", "XDG_DOWNLOAD_DIR": "/private/downloads"}} {
			s, err := New(Options{Home: home, Platform: platform, Environment: func(k string) (string, bool) { v, ok := env[k]; return v, ok }, LoadUserConfig: func(context.Context, string, string) (*protocol.JSONObject, error) { return nil, nil }, SaveUserConfig: func(context.Context, string, string, protocol.JSONObject, protocol.JSONObject) error { return nil }, Now: time.Now})
			if err != nil {
				t.Fatal(err)
			}
			cases = append(cases, map[string]any{"action": "dirs", "env": env, "platform": platform})
			observations = append(observations, observe(s.SystemDirectories(), nil, nil))
		}
	}
	sourceCompare(t, home, cases, observations)
}
