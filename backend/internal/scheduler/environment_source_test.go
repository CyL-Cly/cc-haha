package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestSchedulerEnvironmentActualSourceUndefinedNullProviderAndPrecedence(t *testing.T) {
	process := map[string]string{"PATH": "/fixture/bin", "HOME": "/fixture/home", "CLAUDE_CODE_OAUTH_TOKEN": "fake inherited old oauth", "CC_HAHA_AGENT_TEAMS_ENABLED": "stale", "ANTHROPIC_AUTH_TOKEN": "fake inherited token", "ANTHROPIC_MODEL": "claude-old", "ANTHROPIC_BASE_URL": "https://fixture.invalid", "OTHER": "preserved", "CLAUDE_CODE_USE_BEDROCK": "1", "AWS_BEARER_TOKEN_BEDROCK": "fake", "DEEPSEEK_API_KEY": "fake"}
	rows := []map[string]any{}
	for _, task := range []map[string]any{{}, {"providerId": nil}, {"providerId": "saved"}, {"providerId": "saved", "model": "  deepseek-flash  "}, {"model": " Claude-override "}, {"model": " "}, {"providerId": false}, {"providerId": 42}, {"providerId": map[string]any{}}, {"providerId": []any{}}} {
		for _, state := range []map[string]any{{}, {"hasProviders": true}, {"settings": map[string]any{"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "fake desktop"}}}, {"settings": map[string]any{"env": map[string]any{"CLAUDE_CODE_USE_BEDROCK": "1"}}}, {"settings": "bad"}, {"settings": "\ufeff{}"}} {
			row := map[string]any{"task": task, "processEnv": process, "providerEnv": map[string]string{"ANTHROPIC_AUTH_TOKEN": "fake provider", "ANTHROPIC_MODEL": "deepseek-flash", "CLAUDE_CODE_ENTRYPOINT": "sdk-cli", "OTHER": "provider override"}, "networkEnv": map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:3456", "HTTPS_PROXY": "fixture proxy"}, "teams": true, "oauthToken": "fake-official"}
			for k, v := range state {
				row[k] = v
			}
			rows = append(rows, row)
		}
	}
	row := map[string]any{"task": map[string]any{"providerId": nil}, "processEnv": process, "providerEnv": nil, "networkEnv": map[string]string{}, "teams": false, "oauthError": "fixture secure token unavailable"}
	rows = append(rows, row)
	data := actualSource(t, "environment", map[string]any{"rows": rows})
	var source struct {
		Rows []struct {
			Env, NetworkInput map[string]string
			Error             string
		}
	}
	if e := json.Unmarshal(data, &source); e != nil {
		t.Fatal(e)
	}
	for n, row := range rows {
		home := t.TempDir()
		config := filepath.Join(home, ".claude")
		_ = os.MkdirAll(filepath.Join(config, "cc-haha"), 0700)
		if state, ok := row["settings"]; ok {
			var b []byte
			if text, ok := state.(string); ok {
				b = []byte(text)
			} else {
				b = raw(state)
			}
			_ = os.WriteFile(filepath.Join(config, "cc-haha/settings.json"), b, 0600)
		}
		if row["hasProviders"] == true {
			_ = os.WriteFile(filepath.Join(config, "cc-haha/providers.json"), []byte(`{}`), 0600)
		}
		var networkInput map[string]string
		var logs []error
		o := Options{Home: home, ConfigDir: config, Files: OSFiles{}, GetProcessEnvironment: func(context.Context) (map[string]string, error) { return cloneEnvironment(process), nil }, ProviderRuntimeEnv: func(context.Context, string) (map[string]string, error) {
			if row["providerEnv"] == nil {
				return nil, nil
			}
			return cloneEnvironment(row["providerEnv"].(map[string]string)), nil
		}, NetworkEnvironment: func(_ context.Context, env map[string]string) (map[string]string, error) {
			networkInput = env
			return row["networkEnv"].(map[string]string), nil
		}, AgentTeamsEnabled: func(context.Context) (bool, error) { return row["teams"].(bool), nil }, OfficialOAuthEnv: func(context.Context) (map[string]string, error) {
			if row["oauthError"] != nil {
				return nil, errors.New(row["oauthError"].(string))
			}
			env := map[string]string{"CLAUDE_CODE_ENTRYPOINT": "claude-desktop"}
			if token, ok := row["oauthToken"].(string); ok && token != "" {
				env["CLAUDE_CODE_OAUTH_TOKEN"] = token
			}
			return env, nil
		}, LogError: func(e error) { logs = append(logs, e) }}
		var task protocol.JSONObject
		_ = task.UnmarshalJSON(raw(row["task"]))
		env, e := BuildTaskEnvironment(context.Background(), o, task, "/fixture/work")
		if e != nil || string(raw(env)) != string(raw(source.Rows[n].Env)) || string(raw(networkInput)) != string(raw(source.Rows[n].NetworkInput)) {
			t.Errorf("case%d Go%s source%s network%s/%s error%v", n, raw(env), raw(source.Rows[n].Env), raw(networkInput), raw(source.Rows[n].NetworkInput), e)
		}
		if row["oauthError"] != nil && len(logs) != 1 {
			t.Fatal("original OAuth failure logging lost", logs)
		}
	}
}
