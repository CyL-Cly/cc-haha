package openaiauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func authJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func authObject(raw json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(raw)
	return o
}

type faultFiles struct {
	PhysicalFiles
	fault string
}

func (f faultFiles) MkdirAll(p string, m fs.FileMode) error {
	if f.fault == "mkdir" {
		return errors.New("private mkdir fault")
	}
	return f.PhysicalFiles.MkdirAll(p, m)
}
func (f faultFiles) WriteFile(p string, b []byte, m fs.FileMode) error {
	marker := strings.HasSuffix(p, MarkerFile)
	if f.fault == "write" && !marker || f.fault == "marker" && marker {
		return errors.New("private write fault")
	}
	return f.PhysicalFiles.WriteFile(p, b, m)
}
func (f faultFiles) Rename(a, b string) error {
	if f.fault == "rename" {
		return errors.New("private rename fault")
	}
	return f.PhysicalFiles.Rename(a, b)
}

func TestCLIAuthStorageMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	now := int64(1770000000000)
	valid := `{"accessToken":"fake-old","refreshToken":"fake-refresh","expiresAt":1770003600000,"email":null,"future":{"keep":true}}`
	expired := `{"accessToken":"fake-old","refreshToken":"fake-refresh","expiresAt":1769999999999,"email":"fake@invalid","clientId":"legacy-client","future":42}`
	act := func(op string, value any) map[string]any { return map[string]any{"op": op, "value": value} }
	rows := []map[string]any{}
	for _, file := range []any{nil, "null", "false", "42", `"primitive"`, `[]`, `{}`, valid, expired, `{"accessToken":"","refreshToken":"","expiresAt":-1,"idToken":"","accountId":false,"clientId":""}`, `{"accessToken":"\ud800","refreshToken":"\udfff","expiresAt":1e999}`, "\ufeff" + valid, `{"accessToken":"a","refreshToken":"b","expiresAt":"1770003600000"}`, "broken"} {
		row := map[string]any{"actions": []any{act("get", nil), act("async", nil)}}
		if file != nil {
			row["file"] = file
		}
		rows = append(rows, row)
	}
	for _, tokens := range []any{nil, false, 0, "", true, "primitive", []any{1}, map[string]any{}, authObject([]byte(valid))} {
		rows = append(rows, map[string]any{"env": nil, "secure": map[string]any{StorageKey: tokens, "otherAccount": map[string]any{"keep": true}}, "actions": []any{act("get", nil), act("async", nil), act("ensure", nil)}})
	}
	rows = append(rows,
		map[string]any{"file": valid, "actions": []any{act("get", nil), act("file", expired), act("get", nil), act("async", nil), act("clear", nil), act("get", nil)}},
		map[string]any{"file": valid, "actions": []any{act("get", nil), act("mutate", "changed-ref"), act("get", nil)}},
		map[string]any{"file": valid, "env": "\ufeff \u00a0tokens.json\u2029", "actions": []any{act("get", nil), act("async", nil)}},
		map[string]any{"file": valid, "secure": map[string]any{StorageKey: map[string]any{"accessToken": "stale-secure"}}, "actions": []any{act("delete", nil), act("get", nil), act("env", nil), act("get", nil)}},
		map[string]any{"env": nil, "marker": true, "secure": map[string]any{StorageKey: map[string]any{"accessToken": "stale-secure"}}, "actions": []any{act("get", nil), act("async", nil), act("save", authObject([]byte(valid))), act("get", nil)}},
		map[string]any{"env": nil, "marker": true, "updateSuccess": false, "secure": map[string]any{StorageKey: map[string]any{"accessToken": "stale-secure"}}, "actions": []any{act("save", authObject([]byte(valid))), act("get", nil), act("delete", nil), act("get", nil)}},
		map[string]any{"env": nil, "secure": map[string]any{StorageKey: map[string]any{"accessToken": "old"}, "future": []any{1, "keep"}}, "warning": "private warning", "actions": []any{act("get", nil), act("save", authObject([]byte(valid))), act("get", nil), act("delete", nil), act("get", nil)}},
		map[string]any{"env": nil, "readError": "private read fault", "actions": []any{act("get", nil), act("async", nil), act("save", authObject([]byte(valid))), act("delete", nil)}},
		map[string]any{"env": nil, "updateError": "private update fault", "actions": []any{act("save", authObject([]byte(valid))), act("delete", nil)}},
	)
	for _, secure := range []any{false, 0, "string", []any{1}} {
		rows = append(rows, map[string]any{"env": nil, "secure": secure, "actions": []any{act("save", authObject([]byte(valid))), act("delete", nil)}})
	}
	for _, fault := range []string{"mkdir", "write", "rename", "marker"} {
		rows = append(rows, map[string]any{"file": valid, "fault": fault, "actions": []any{act("save", authObject([]byte(expired))), act("get", nil)}})
	}
	for _, row := range []map[string]any{
		{"file": expired, "response": map[string]any{"access_token": "fake-new", "expires_in": 3600}},
		{"env": nil, "secure": map[string]any{StorageKey: authObject([]byte(expired)), "keep": true}, "response": map[string]any{"access_token": "fake-new", "refresh_token": "new-refresh", "expires_in": 0}},
		{"file": expired, "refreshError": "private refresh fault"},
		{"file": expired, "fault": "rename", "response": map[string]any{"access_token": "fake-new", "expires_in": 3600}},
		{"env": nil, "secure": map[string]any{StorageKey: authObject([]byte(expired))}, "updateSuccess": false, "response": map[string]any{"access_token": "fake-new", "expires_in": 3600}},
	} {
		row["actions"] = []any{act("get", nil), act("ensure", nil), act("get", nil)}
		rows = append(rows, row)
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-openai-storage.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_OPENAI_STORAGE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(authJSON(map[string]any{"now": now, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	source, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source: %v %s", err, stderr.String())
	}
	var expected []json.RawMessage
	if json.Unmarshal(source, &expected) != nil || len(expected) != len(rows) {
		t.Fatalf("Source shape: %s", source)
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			root := t.TempDir()
			config := filepath.Join(root, ".claude")
			file := filepath.Join(root, "tokens.json")
			env := map[string]string{"CLAUDE_CONFIG_DIR": config, FileEnvironmentKey: file}
			if e, present := row["env"]; present {
				if e == nil {
					delete(env, FileEnvironmentKey)
				} else {
					env[FileEnvironmentKey] = e.(string)
				}
			}
			secure := authJSON(row["secure"])
			calls := []any{}
			logs := 0
			options := Options{Home: root, CWD: root, Environment: func() map[string]string { return env }, Files: faultFiles{fault: fmt.Sprint(row["fault"])}, NowMillis: func() int64 { return now }, PID: 17, LogError: func(error) { logs++ }}
			options.SecureRead = func(context.Context) (json.RawMessage, error) {
				calls = append(calls, "sync")
				if e := row["readError"]; e != nil {
					return nil, errors.New(e.(string))
				}
				return append(json.RawMessage(nil), secure...), nil
			}
			options.SecureReadAsync = func(context.Context) (json.RawMessage, error) {
				calls = append(calls, "async")
				if e := row["readError"]; e != nil {
					return nil, errors.New(e.(string))
				}
				return append(json.RawMessage(nil), secure...), nil
			}
			options.SecureUpdate = func(_ context.Context, value json.RawMessage) (Result, error) {
				calls = append(calls, map[string]any{"update": json.RawMessage(append([]byte(nil), value...))})
				if e := row["updateError"]; e != nil {
					return Result{}, errors.New(e.(string))
				}
				success := row["updateSuccess"] != false
				if success {
					secure = append(json.RawMessage(nil), value...)
				}
				result := Result{Success: success}
				if w, p := row["warning"]; p {
					s := w.(string)
					result.Warning = &s
				}
				return result, nil
			}
			options.Refresh = func(_ context.Context, token json.RawMessage) (protocol.JSONObject, error) {
				call := protocol.NewJSONObject(protocol.JSONField{Name: "refresh", Value: token})
				calls = append(calls, call)
				if e := row["refreshError"]; e != nil {
					return protocol.JSONObject{}, errors.New(e.(string))
				}
				return authObject(authJSON(row["response"])), nil
			}
			service, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			if raw, present := row["file"]; present {
				if err = os.WriteFile(file, []byte(raw.(string)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if row["marker"] == true {
				if err = os.MkdirAll(filepath.Dir(service.MarkerPath()), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(service.MarkerPath(), []byte("1\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			values := []any{}
			var last *Value
			// Keep object references until final serialization, as actual Source does.
			for _, a := range row["actions"].([]any) {
				action := a.(map[string]any)
				switch action["op"] {
				case "get":
					last = service.GetCached(ctx)
					values = append(values, last)
				case "async":
					last, err = service.GetAsync(ctx)
					if err != nil {
						t.Fatal(err)
					}
					values = append(values, last)
				case "ensure":
					last, err = service.EnsureFresh(ctx)
					if err != nil {
						t.Fatal(err)
					}
					values = append(values, last)
				case "save":
					v, e := NewValue(authJSON(action["value"]))
					if e != nil {
						t.Fatal(e)
					}
					values = append(values, service.SaveValue(ctx, v))
				case "delete":
					values = append(values, service.Delete(ctx))
				case "clear":
					service.ClearCache()
				case "file":
					if err = os.WriteFile(file, []byte(action["value"].(string)), 0600); err != nil {
						t.Fatal(err)
					}
				case "env":
					if action["value"] == nil {
						delete(env, FileEnvironmentKey)
					} else {
						env[FileEnvironmentKey] = action["value"].(string)
					}
				case "secure":
					secure = authJSON(action["value"])
				case "mutate":
					if last != nil {
						last.Object.Set("accessToken", authJSON(action["value"]))
					}
				}
			}
			for j, value := range values {
				if v, ok := value.(*Value); ok {
					values[j] = v.JSON()
				}
			}
			result := map[string]any{"values": values, "calls": calls, "logs": logs, "secure": secure, "marker": PhysicalFiles{}.Exists(service.MarkerPath()), "file": nil, "mode": nil, "temporaries": []string{}}
			if data, e := os.ReadFile(file); e == nil {
				result["file"] = string(data)
				info, e := os.Stat(file)
				if e != nil {
					t.Fatal(e)
				}
				result["mode"] = uint32(info.Mode().Perm())
			}
			entries, e := os.ReadDir(root)
			if e != nil {
				t.Fatal(e)
			}
			for _, entry := range entries {
				if strings.Contains(entry.Name(), ".tmp.") {
					result["temporaries"] = append(result["temporaries"].([]string), entry.Name())
				}
			}
			var got, want any
			_ = json.Unmarshal(authJSON(result), &got)
			_ = json.Unmarshal(expected[i], &want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Go %s\nSource %s", authJSON(result), expected[i])
			}
		})
	}
}
