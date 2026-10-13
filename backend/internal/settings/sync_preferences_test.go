package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSyncPreferencesDefaultsDoNotRewriteOldSettings(t *testing.T) {
	options := fixtureOptions(t)
	s := mustService(t, options)
	path := filepath.Join(options.Environment()["CLAUDE_CONFIG_DIR"], "settings.json")
	old := `{"future":{"keep":true},"maxConcurrentSubagents":-1}`
	seed(t, path, old)
	auto, e := s.GetAutoUpdateEnabled(testContext)
	limit, le := s.GetMaxConcurrentSubagents(testContext)
	if e != nil || le != nil || !auto || limit != nil {
		t.Fatal(auto, limit, e, le)
	}
	data, _ := os.ReadFile(path)
	if string(data) != old {
		t.Fatal("read changed old preferences", string(data))
	}
	for _, raw := range []string{`0`, `-1`, `1.5`, `"2"`, `true`, `{}`, `9007199254740992`, `1e400`} {
		if e = s.UpdateUserSettings(testContext, object(`{"maxConcurrentSubagents":`+raw+`}`)); e == nil {
			t.Fatal("invalid limit", raw)
		}
	}
	for _, raw := range []string{`null`, `0`, `"false"`} {
		if e = s.UpdateUserSettings(testContext, object(`{"autoUpdateEnabled":`+raw+`}`)); e == nil {
			t.Fatal("invalid auto-update", raw)
		}
	}
	for _, raw := range []string{`1`, `9007199254740991`, `null`} {
		if e = s.UpdateUserSettings(testContext, object(`{"maxConcurrentSubagents":`+raw+`,"autoUpdateEnabled":false}`)); e != nil {
			t.Fatal(e)
		}
	}
	data, _ = os.ReadFile(path)
	var result map[string]json.RawMessage
	_ = json.Unmarshal(data, &result)
	if string(objectValue(result["future"])["keep"]) != `true` {
		t.Fatal(string(data))
	}
	auto, _ = s.GetAutoUpdateEnabled(testContext)
	if auto {
		t.Fatal("explicit false lost")
	}
}
