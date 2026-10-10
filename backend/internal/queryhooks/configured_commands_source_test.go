package queryhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestSettingsStopSelectionSnapshotPolicyAndDedupMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	object := func(raw string) protocol.JSONObject {
		var o protocol.JSONObject
		if e := o.UnmarshalJSON([]byte(raw)); e != nil {
			t.Fatal(e)
		}
		return o
	}
	hooks := `{"Stop":[{"matcher":"ignored","hooks":[{"type":"command","command":"first","statusMessage":"old"},{"type":"command","command":"second"},{"type":"command","command":"first","shell":"bash","statusMessage":"last"},{"type":"command","command":"filtered","if":"Read(*)"}]}],"SubagentStop":[{"matcher":"review|worker","hooks":[{"type":"command","command":"child"}]},{"matcher":"^Task$","hooks":[{"type":"command","command":"legacy"}]},{"matcher":"[","hooks":[{"type":"command","command":"invalid"}]}]}`
	settings := object(`{"hooks":` + hooks + `}`)
	session := []protocol.JSONObject{object(`{"hooks":[{"type":"command","command":"session"},{"type":"command","command":"first","statusMessage":"session wins"}]} `)}
	profiles := []protocol.JSONObject{}
	add := func(event, agent, policy string, disabled bool) {
		s := settings.Clone()
		if disabled {
			s.Set("disableAllHooks", []byte("true"))
		}
		p := protocol.NewJSONObject(protocol.JSONField{Name: "event", Value: commandJSON(event)}, protocol.JSONField{Name: "agentType", Value: commandJSON(agent)}, protocol.JSONField{Name: "settings", Value: commandJSON(s)}, protocol.JSONField{Name: "session", Value: commandJSON(session)})
		if policy != "" {
			p.Set("policy", []byte(policy))
		}
		profiles = append(profiles, p)
	}
	for _, event := range []string{"Stop", "SubagentStop"} {
		for _, policy := range []string{"", `{"allowManagedHooksOnly":true,"hooks":{"Stop":[{"hooks":[{"type":"command","command":"managed"}]}]}}`, `{"disableAllHooks":true}`, `{"strictPluginOnlyCustomization":["hooks"]}`, `{"strictPluginOnlyCustomization":["skills"]}`} {
			add(event, "review", policy, false)
		}
		add(event, "review", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"managed despite user disable"}]}]}}`, true)
	}
	add("SubagentStop", "Agent", "", false)
	add("SubagentStop", "", "", false)
	add("SubagentStop", "other", "", false)
	home := t.TempDir()
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-hook-command.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_HOOK_COMMAND_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(commandJSON(map[string]any{"homeDir": home, "selection": profiles}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct{ Rows []protocol.JSONObject }
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(profiles) {
		t.Fatal(e, string(out))
	}
	for i, p := range profiles {
		merged, _ := p.Object("settings")
		policy, _ := p.Object("policy")
		s := &Service{options: Options{Commands: &CommandBindings{Store: &CommandStore{events: map[string]map[string][]commandMatcher{}}}, MergedSettings: func() (*protocol.JSONObject, error) { return &merged, nil }, PolicySettings: func() (*protocol.JSONObject, error) { return &policy, nil }}, registry: registryState{}}
		commands := []commandMatcher{}
		for _, row := range session {
			var list []protocol.JSONObject
			_ = json.Unmarshal(row.Get("hooks"), &list)
			for _, hook := range list {
				commands = append(commands, commandMatcher{row.String("matcher"), hook})
			}
		}
		s.options.Commands.Store.events["actual-agent"] = map[string][]commandMatcher{p.String("event"): commands}
		snapshot, e := s.snapshot()
		if e != nil {
			t.Fatal(e)
		}
		rows, e := s.configuredNativeCommands(p.String("event"), "actual-agent")
		if e != nil {
			t.Fatal(e)
		}
		selected := []protocol.JSONObject{}
		if !s.resolver.disabled {
			selected, e = selectNativeStopCommands(p.String("event"), protocol.NewJSONObject(protocol.JSONField{Name: "agent_type", Value: p.Get("agentType")}), rows)
			if e != nil {
				t.Fatal(e)
			}
		}
		actual := commandJSON(map[string]any{"config": snapshot, "managedOnly": s.resolver.managedOnly, "disabled": s.resolver.disabled, "hooks": selected})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		raw, _ := source.Rows[i].MarshalJSON()
		_ = json.Unmarshal(raw, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("profile%d Go%s Source%s", i, actual, raw)
		}
	}
}
