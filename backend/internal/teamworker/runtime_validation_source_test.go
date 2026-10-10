package teamworker

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
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamplan"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func validationObject(v any) protocol.JSONObject {
	var out protocol.JSONObject
	_ = out.UnmarshalJSON(raw(v))
	return out
}
func TestTeamApprovalLocalValidationMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	provider := validationObject(map[string]any{"id": "private-provider", "presetId": "deepseek", "name": "Private DeepSeek", "apiKey": "fake-key", "baseUrl": "http://127.0.0.1:1/anthropic", "apiFormat": "anthropic", "models": map[string]string{"main": "deepseek-v4-flash", "haiku": "", "sonnet": "deepseek-v4-flash", "opus": "deepseek-v4-pro"}})
	snapshot := validationObject(map[string]any{"source": "built-in", "sourceIdentity": map[string]string{"kind": "builtin"}, "systemPrompt": "Frozen real system prompt", "tools": []string{"Read"}})
	runtime := validationObject(map[string]any{"providerId": "private-provider", "modelId": "default", "effortLevel": "max"})
	member := validationObject(map[string]any{"id": "m1", "name": "reader", "agentType": "read-preset", "prompt": "Perform actual task", "runtime": runtime})
	base := validationObject(map[string]any{"workDir": home, "sessionId": "leader", "teamName": "", "members": []protocol.JSONObject{member}, "agentCatalog": map[string]any{"read-preset": snapshot}})
	type testCase struct {
		Name      string                `json:"name"`
		Plan      protocol.JSONObject   `json:"plan"`
		Providers []protocol.JSONObject `json:"providers"`
		Team      *protocol.JSONObject  `json:"team,omitempty"`
		Stale     bool                  `json:"stale,omitempty"`
		Mode      string                `json:"permissionMode,omitempty"`
	}
	cases := []testCase{}
	add := func(name string, mutate func(*protocol.JSONObject, *protocol.JSONObject, *protocol.JSONObject, *protocol.JSONObject)) {
		p, m, s, r := base.Clone(), member.Clone(), snapshot.Clone(), runtime.Clone()
		if mutate != nil {
			mutate(&p, &m, &s, &r)
		}
		m.Set("runtime", raw(r))
		p.Set("members", raw([]protocol.JSONObject{m}))
		p.Set("agentCatalog", raw(map[string]any{"read-preset": s}))
		cases = append(cases, testCase{Name: name, Plan: p, Providers: []protocol.JSONObject{provider}})
	}
	add("mapped-default", nil)
	add("concrete", func(_, _, _, r *protocol.JSONObject) { r.Set("modelId", raw("namespace/model")) })
	add("missing-mapping", func(_, _, _, r *protocol.JSONObject) { r.Set("modelId", raw("haiku")) })
	add("missing-provider", func(_, _, _, r *protocol.JSONObject) { r.Set("providerId", raw("deleted")) })
	for _, value := range []string{"inherit", "a b", "a\tmodel", "a\u00a0model", "a\ufeffmodel", "a\u2028model", "a\u0085model", "  main  ", "default[1m]"} {
		value := value
		add("concrete-"+value, func(_, _, _, r *protocol.JSONObject) { r.Set("modelId", raw(value)) })
	}
	for _, value := range []string{"sonnet[1m]", "Opus:1M", "claude-opus-4-7-20260416", "not-claude", "default", "inherit"} {
		value := value
		add("official-"+value, func(_, _, _, r *protocol.JSONObject) {
			r.Set("providerId", raw("claude-official"))
			r.Set("modelId", raw(value))
		})
	}
	for _, value := range []string{"", "high", "ultra"} {
		value := value
		add("effort-"+value, func(_, _, _, r *protocol.JSONObject) { r.Set("effortLevel", raw(value)) })
	}
	add("invalid-name", func(_, m, _, _ *protocol.JSONObject) { m.Set("name", raw("README Reader")) })
	add("missing-preset", func(_, m, _, _ *protocol.JSONObject) { m.Set("agentType", raw("absent")) })
	add("empty-prompt", func(_, _, s, _ *protocol.JSONObject) { s.Set("systemPrompt", raw(" \ufeff\n")) })
	add("missing-source", func(_, _, s, _ *protocol.JSONObject) { s.Delete("sourceIdentity") })
	add("configuration", func(_, _, s, _ *protocol.JSONObject) {
		s.Set("configurationError", raw("Original configured server missing"))
	})
	add("permission", func(_, _, s, _ *protocol.JSONObject) { s.Set("permissionMode", raw("plan")) })
	add("isolation", func(_, _, s, _ *protocol.JSONObject) { s.Set("isolation", raw("worktree")) })
	for _, value := range []any{0, 1, 2.5, "3", nil} {
		value := value
		add("maxTurns", func(_, _, s, _ *protocol.JSONObject) { s.Set("maxTurns", raw(value)) })
	}
	add("essential-denied", func(_, _, s, _ *protocol.JSONObject) {
		s.Set("disallowedTools", raw([]string{"Read", "TaskUpdate(*)"}))
	})
	add("other-denied", func(_, _, s, _ *protocol.JSONObject) { s.Set("disallowedTools", raw([]string{"Read", "Agent"})) })
	add("duplicates", nil)
	duplicate := cases[len(cases)-1].Plan.Clone()
	duplicate.Set("members", raw([]protocol.JSONObject{member, member}))
	cases[len(cases)-1].Plan = duplicate
	team := validationObject(map[string]any{"name": "source-team", "leadSessionId": "leader", "createdAt": 1000, "members": []any{}})
	for _, kind := range []string{"valid-generation", "stale-generation", "member-collision"} {
		add(kind, nil)
		c := &cases[len(cases)-1]
		c.Plan.Set("teamName", raw("source-team"))
		c.Plan.Set("incarnationId", raw("stale"))
		v := team.Clone()
		if kind == "member-collision" {
			v.Set("members", raw([]any{map[string]string{"name": "reader"}}))
		}
		c.Team = &v
		c.Stale = kind == "stale-generation"
	}
	for _, kind := range []string{"zhipuglm", "zhipu-coding-plan"} {
		add(kind, nil)
		c := &cases[len(cases)-1]
		p := provider.Clone()
		p.Set("presetId", raw(kind))
		p.Set("models", raw(map[string]string{"main": "glm-5.3", "haiku": "", "sonnet": "glm-5.3", "opus": "glm-5.3"}))
		c.Providers = []protocol.JSONObject{p}
		rows := []protocol.JSONObject{}
		_ = json.Unmarshal(c.Plan.Get("members"), &rows)
		r := validationObject(rows[0].Get("runtime"))
		r.Set("effortLevel", raw("medium"))
		rows[0].Set("runtime", raw(r))
		c.Plan.Set("members", raw(rows))
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-runtime-validation.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_VALIDATION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
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
	for i, c := range cases {
		configDir := filepath.Join(t.TempDir(), ".claude")
		_ = os.MkdirAll(filepath.Join(configDir, "cc-haha"), 0700)
		_ = os.WriteFile(filepath.Join(configDir, "cc-haha", "providers.json"), raw(map[string]any{"schemaVersion": 6, "providers": c.Providers, "activeId": nil, "providerOrder": []string{}, "officialProviderModels": map[string]any{}}), 0600)
		providers, _ := providerconfig.New(providerconfig.Options{Store: storage.New(storage.Options{}), ConfigDir: configDir})
		tasks, _ := taskstore.New(taskstore.Options{ConfigDir: configDir, PID: os.Getpid(), Now: time.Now})
		config, _ := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: configDir, TaskStore: tasks, PID: os.Getpid()})
		plans, _ := teamplan.New(teamplan.Options{Config: config, Tasks: tasks, ConfigDir: configDir, WorkDir: home, Now: time.Now, UUID: func() (string, error) { return "private", nil }})
		p := c.Plan.Clone()
		if c.Team != nil {
			if err = config.Write(p.String("teamName"), *c.Team); err != nil {
				t.Fatal(err)
			}
			if !c.Stale {
				inc, _ := plans.Incarnation(p.String("teamName"), p.String("sessionId"))
				p.Set("incarnationId", raw(inc))
			}
		}
		value, err := ValidateRuntime(ctx, p, ValidationOptions{Config: config, Plans: plans, PermissionMode: func(context.Context, string) (string, error) {
			if c.Mode != "" {
				return c.Mode, nil
			}
			return "default", nil
		}, Routing: providers.TeamRuntime})
		var v any
		var message any
		if err != nil {
			message = err.Error()
		} else {
			v = value
		}
		actual := raw(map[string]any{"value": v, "error": message})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%d %s Go %s\nSource %s", i, c.Name, actual, source.Rows[i])
		}
	}
}
