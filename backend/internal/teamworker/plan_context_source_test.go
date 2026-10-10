package teamworker

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentbuiltinmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestFrozenProposalModelPrecedencePresetsAndResultMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	definitions := validationObject(map[string]any{"pinned": map[string]any{"description": "Pin actual Agent", "prompt": "RAW_PRESET_BODY", "model": "opus", "effort": "low", "permissionMode": "default", "tools": []string{}, "disallowedTools": []string{"Bash"}, "skills": []string{"original"}, "hooks": map[string]any{"Stop": []any{}}, "mcpServers": []any{"named"}, "maxTurns": 3, "memory": "project"}, "inherit": map[string]any{"description": "Inherit", "prompt": "INHERITED_PRESET_BODY", "model": "inherit", "omitClaudeMd": false}, "inline": map[string]any{"description": "Inline", "prompt": "INLINE_PRESET_BODY", "mcpServers": []any{"named", map[string]any{"inline": map[string]string{"type": "http", "url": "http://127.0.0.1:1"}}}}})
	baseProposal := validationObject(map[string]any{"members": []any{map[string]any{"id": "member", "name": "worker", "prompt": "Complete actual task", "agentType": "pinned"}}, "tasks": []any{map[string]any{"id": "task", "subject": "Actual", "ownerId": "member", "dependencies": []string{}}}})
	leader := string(raw(map[string]string{"providerId": "private-provider", "modelId": "saved-leader", "effortLevel": "high"}))
	cases := []protocol.JSONObject{}
	add := func(runtime, model, subagent, kind, state string) {
		p := baseProposal.Clone()
		members := rows(p.Get("members"))
		members[0].Set("agentType", raw(kind))
		p.Set("members", raw(members))
		c := validationObject(map[string]any{"runtime": runtime, "model": model, "agents": definitions, "proposal": p, "state": state})
		if subagent != "" {
			c.Set("subagent", raw(subagent))
		}
		cases = append(cases, c)
	}
	for _, subagent := range []string{"", "haiku", " InHeRiT ", "concrete-override"} {
		for _, kind := range []string{"pinned", "inherit", "general-purpose", "Explore"} {
			add(leader, "executing-leader", subagent, kind, "draft")
		}
	}
	add(leader, "", "", "inherit", "review_pending")
	add(leader, "executing-leader", "", "inline", "draft")
	add(leader, "executing-leader", "", "missing", "draft")
	for _, runtime := range []string{"bad-json", "null", "{}", `{"providerId":""}`, `{"providerId":"private","modelId":""}`} {
		add(runtime, "", "", "pinned", "draft")
	}
	add(leader, "executing-leader", "haiku", "pinned", "running")
	last := &cases[len(cases)-1]
	p := object(last.Get("proposal"))
	members := rows(p.Get("members"))
	members[0].Set("runtime", raw(map[string]string{"providerId": "human-other", "modelId": "human-concrete", "effortLevel": "max"}))
	p.Set("members", raw(members))
	last.Set("proposal", raw(p))
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-plan-context.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_CONTEXT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=true", "ANTHROPIC_API_KEY=fake-private", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "ANTHROPIC_DEFAULT_OPUS_MODEL=mapped-opus", "ANTHROPIC_DEFAULT_HAIKU_MODEL=mapped-haiku", "ANTHROPIC_DEFAULT_SONNET_MODEL=mapped-sonnet", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "cases": cases}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	active := []agentcatalog.Definition{}
	for _, stock := range agentbuiltinmetadata.Stock() {
		name := stock.Fields.String("agentType")
		if name == "general-purpose" || name == "Explore" {
			active = append(active, agentcatalog.Definition{Name: name, Source: stock.Fields.String("source"), Description: stock.Fields.String("whenToUse"), Fields: stock.Fields.Clone(), SystemPrompt: stock.SystemPrompt})
		}
	}
	for _, property := range definitions.OwnProperties() {
		var name string
		_ = json.Unmarshal(property.NameJSON, &name)
		d, err := agentcatalog.ParseJSONDefinition(name, property.Value, "flagSettings", "external", false)
		if err != nil || d == nil {
			t.Fatal(err)
		}
		active = append(active, *d)
	}
	for i, c := range cases {
		env := map[string]string{"CC_HAHA_TEAM_LEADER_RUNTIME": c.String("runtime"), "CLAUDE_CODE_SUBAGENT_MODEL": c.String("subagent"), "ANTHROPIC_API_KEY": "fake-private", "ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "ANTHROPIC_DEFAULT_OPUS_MODEL": "mapped-opus", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "mapped-haiku", "ANTHROPIC_DEFAULT_SONNET_MODEL": "mapped-sonnet"}
		observed := protocol.NewJSONObject()
		leader, err := LeaderRuntime(env, c.String("model"))
		if err == nil {
			var catalog, proposal protocol.JSONObject
			catalog, err = SnapshotAgents(active, func(d agentcatalog.Definition) (string, error) { return d.SystemPrompt, nil })
			if err == nil {
				selector, _ := modelselection.NewDirectCredentials(modelselection.Options{Environment: env, Settings: protocol.NewJSONObject()})
				proposal, err = ResolveProposal(object(c.Get("proposal")), catalog, leader, env, func(v string) (string, error) {
					out, e := selector.Parse(raw(v))
					if e != nil {
						return "", e
					}
					return object(raw(map[string]json.RawMessage{"v": out})).String("v"), nil
				})
			}
			if err == nil {
				plan := protocol.NewJSONObject(protocol.JSONField{Name: "teamName", Value: raw("actual-context")}, protocol.JSONField{Name: "planId", Value: raw("durable-plan")}, protocol.JSONField{Name: "revision", Value: raw(3)}, protocol.JSONField{Name: "state", Value: c.Get("state")}, protocol.JSONField{Name: "members", Value: proposal.Get("members")}, protocol.JSONField{Name: "tasks", Value: proposal.Get("tasks")})
				observed = validationObject(map[string]any{"leader": leader, "catalog": catalog, "proposal": proposal, "result": PlanToolResult(plan), "error": nil})
			}
		}
		if err != nil {
			observed = validationObject(map[string]string{"error": err.Error()})
		}
		var a, b any
		_ = json.Unmarshal(raw(observed), &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case%d Go%s\nSource%s", i, raw(observed), source.Rows[i])
		}
	}
}
