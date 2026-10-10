package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestSchedulerStoreActualSourceCRUDBytesUnknownFieldsAndMalformedFile(t *testing.T) {
	rows := []map[string]any{
		{"initial": `{"future":{"keep":true},"tasks":[{"id":"old","createdAt":7,"cron":"* * * * *","prompt":"old","permissionMode":"plan","future":"keep"}]}`, "actions": []map[string]any{{"type": "list"}, {"type": "update", "id": "old", "input": map[string]any{"id": "wrong", "createdAt": 999, "enabled": false, "permissionMode": "default", "future2": 3}}, {"type": "lastFired", "id": "old", "timestamp": "2026-06-01T12:00:00.000Z", "sibling": true}, {"type": "list"}}},
		{"initial": "\ufeff{\"tasks\":[]}", "actions": []map[string]any{{"type": "create", "input": map[string]any{"cron": "*/5 * * * *", "prompt": "actual task", "permissionMode": "plan", "future": 3}}, {"type": "list"}}},
		{"initial": " \x00\ufeff\n", "actions": []map[string]any{{"type": "create", "input": map[string]any{"cron": "* * * * *", "prompt": "second"}}}},
		{"initial": `{"tasks":[{"id":"delete-me","createdAt":5}]}`, "actions": []map[string]any{{"type": "delete", "id": "delete-me"}, {"type": "lastFired", "id": "delete-me", "timestamp": "ignored"}, {"type": "delete", "id": "missing"}}},
		{"initial": `{invalid protected old state`, "actions": []map[string]any{{"type": "list"}, {"type": "create", "input": map[string]any{"cron": "* * * * *", "prompt": "must not overwrite"}}}},
		{"initial": "{}", "actions": []map[string]any{{"type": "create", "input": map[string]any{"cron": "", "prompt": "missing cron"}}, {"type": "create", "input": map[string]any{"cron": "* * * * *", "prompt": ""}}}},
	}
	data := actualSource(t, "store", map[string]any{"now": 1700000000000, "rows": rows})
	var source struct {
		Rows []struct {
			Results []struct {
				Value json.RawMessage
				Error *struct {
					StatusCode    int
					Code, Message string
				}
			}
			File string
		}
	}
	if e := json.Unmarshal(data, &source); e != nil {
		t.Fatal(e)
	}
	random := 0
	for i, row := range rows {
		home := t.TempDir()
		options := Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Files: OSFiles{}, Now: func() time.Time { return time.UnixMilli(1700000000000) }, RandomBytes: func(n int) ([]byte, error) {
			random++
			data := make([]byte, n)
			for i := range data {
				data[i] = byte(random)
			}
			return data, nil
		}}
		store := &taskStore{options}
		_ = os.MkdirAll(options.ConfigDir, 0700)
		_ = os.WriteFile(store.path(), []byte(row["initial"].(string)), 0600)
		for j, action := range row["actions"].([]map[string]any) {
			var value any
			var e error
			id, _ := action["id"].(string)
			var input protocol.JSONObject
			if action["input"] != nil {
				_ = input.UnmarshalJSON(raw(action["input"]))
			}
			switch action["type"] {
			case "list":
				value, e = store.list()
			case "create":
				value, e = store.create(input)
			case "update":
				value, e = store.update(id, input)
			case "delete":
				e = store.delete(id)
			case "lastFired":
				e = store.lastFired(id, action["timestamp"].(string))
			}
			expected := source.Rows[i].Results[j]
			if expected.Error != nil {
				native, ok := e.(*Error)
				if !ok || native.Status != expected.Error.StatusCode || native.Code != expected.Error.Code || native.Message != expected.Error.Message {
					t.Errorf("case%d/%d error Go%v Source%+v", i, j, e, expected.Error)
				}
			} else if e != nil || string(protocol.StringifyValidatedJavaScriptJSON(raw(value))) != string(protocol.StringifyValidatedJavaScriptJSON(expected.Value)) {
				t.Errorf("case%d/%d Go%s %v Source%s", i, j, raw(value), e, expected.Value)
			}
		}
		file, e := os.ReadFile(store.path())
		if e != nil || string(file) != source.Rows[i].File {
			t.Errorf("case%d file Go%s Source%s error%v", i, file, source.Rows[i].File, e)
		}
	}
}
