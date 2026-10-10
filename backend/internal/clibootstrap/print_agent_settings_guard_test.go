package clibootstrap

import (
	"bytes"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintCannotSilentlyConsumeNewActiveAgentSettings(t *testing.T) {
	n := nativeForTest(t)
	agentsWrite(t, filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), `{"builtInAgentOverrides":{"Explore":{"model":"real-override"}}}`)
	args := mainRecipeArgs()
	for i := range args {
		if args[i] == "--setting-sources" {
			args[i+1] = "user"
		}
	}
	a, e := ParsePrintArguments(args, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	m, e := NewMainPrint(MainPrintOptions{Arguments: a, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: &bytes.Buffer{}, Error: &bytes.Buffer{}})
	if m != nil {
		m.Close()
	}
	if e == nil || !strings.Contains(e.Error(), "print active agent settings consumer") {
		t.Fatalf("unported print consumer admitted: %v", e)
	}
}

type printSettingsFixture struct {
	merged, policy protocol.JSONObject
	errorValue     error
}

func (f printSettingsFixture) GetInitialSettings() (*protocol.JSONObject, error) {
	return &f.merged, f.errorValue
}
func (f printSettingsFixture) GetSettingsForSource(string) (*protocol.JSONObject, error) {
	return &f.policy, f.errorValue
}
func TestPrintAgentGuardAdmitsInactiveAndTrustsOnlyManagedPolicy(t *testing.T) {
	for _, raw := range []string{`{}`, `{"builtInAgentOverrides":{}}`, `{"builtInAgentOverrides":{"future":{}}}`, `{"strictPluginOnlyCustomization":true}`} {
		var v protocol.JSONObject
		_ = v.UnmarshalJSON([]byte(raw))
		if e := checkPrintAgentSettings(printSettingsFixture{merged: v, policy: protocol.NewJSONObject()}); e != nil {
			t.Errorf("inactive/untrusted %s=%v", raw, e)
		}
	}
	for _, raw := range []string{`{"strictPluginOnlyCustomization":false}`, `{"strictPluginOnlyCustomization":[]}`, `{"strictPluginOnlyCustomization":["skills","hooks","mcp"]}`} {
		var v protocol.JSONObject
		_ = v.UnmarshalJSON([]byte(raw))
		if e := checkPrintAgentSettings(printSettingsFixture{merged: protocol.NewJSONObject(), policy: v}); e != nil {
			t.Errorf("non-agent managed %s=%v", raw, e)
		}
	}
	for _, raw := range []string{`{"strictPluginOnlyCustomization":true}`, `{"strictPluginOnlyCustomization":["hooks","agents"]}`} {
		var v protocol.JSONObject
		_ = v.UnmarshalJSON([]byte(raw))
		if e := checkPrintAgentSettings(printSettingsFixture{merged: protocol.NewJSONObject(), policy: v}); e == nil {
			t.Errorf("managed active %s silently consumed", raw)
		}
	}
}
