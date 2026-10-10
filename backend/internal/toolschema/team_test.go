package toolschema

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestTeamToolsSchemaPromptAndNestedParsersMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	inputs := map[string][]string{
		"TeamCreate": {`{"team_name":"team"}`, `{"team_name":""}`, `{"team_name":" ","description":"\ud800","agent_type":""}`, `{"team_name":"t","extra":1}`, `{"team_name":null,"description":false,"extra":1}`, `{"team_name":"t","plan":{"members":[],"tasks":[],"extra":1}}`, `{"team_name":"t","plan":{"members":[{"id":"m","name":"worker","prompt":"do","runtime":{"providerId":"p","modelId":"m","effortLevel":"","future":true},"future":true}],"tasks":[{"id":"t","subject":"task","future":true}]}}`, `{"team_name":"t","plan":{"members":[{"id":"","name":1,"agentType":null,"prompt":""}],"tasks":[{},null,1]}}`, `{"team_name":"t","plan":null}`, `[]`, `null`},
		"TeamDelete": {`{}`, `{"0":1,"future":2,"__proto__":3}`, `[]`, `null`},
		"TeamPlan":   {`{"team_name":"t","operation":"get","future":1}`, `{"team_name":"","operation":"invalid","expected_revision":0}`, `{"team_name":"t","operation":"replace","expected_revision":1.5}`, `{"team_name":"t","operation":"submit","expected_revision":9007199254740992}`, `{"team_name":"t","operation":"submit","expected_revision":-9007199254740992}`, `{"team_name":"t","operation":"submit","expected_revision":"1"}`, `{"team_name":"t","operation":"replace","expected_revision":2,"plan":{"members":[{"id":"m","name":"","prompt":"\ud800","difficulty":"invalid","runtime":{}}],"tasks":[{"id":"t","subject":"s","dependencies":[1,null,"x"],"ownerId":2}]}}`, `{"team_name":"t","operation":"replace","plan":{"members":null,"tasks":"bad"}}`, `{}`, `null`, `{"team_name":"t","operation":"submit","plan":{"members":[null,[],{}],"tasks":[{"id":"","subject":"","dependencies":null}]}}`},
	}
	rows := []map[string]string{}
	names := []string{"TeamCreate", "TeamDelete", "TeamPlan"}
	for _, name := range names {
		for _, input := range inputs[name] {
			rows = append(rows, map[string]string{"tool": name, "input": input})
		}
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-tools.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_TOOLS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=fake-private", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(array(map[string]any{"homeDir": home, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows []struct {
			Success      bool
			Data, Issues string
		}
		Profiles [][]struct {
			Schema                    json.RawMessage
			Prompt, Description, Hint string
			Deferred, Always          bool
		}
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(rows) || len(source.Profiles) != 2 || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	for i, row := range rows {
		def, _ := NativeBuiltinDefinition(row["tool"])
		data, failure := ParseInputJSON(def, []byte(row["input"]))
		expected := source.Rows[i]
		if (failure == nil) != expected.Success {
			t.Errorf("row %d Go %v Source %+v", i, failure, expected)
			continue
		}
		if failure == nil {
			if !jsonSourcesEqual(raw(data), []byte(expected.Data)) {
				t.Errorf("row %d Go %s Source %s", i, raw(data), expected.Data)
			}
		} else if validation, ok := failure.(*ValidationError); !ok || !jsonSourcesEqual(validation.Issues, []byte(expected.Issues)) {
			t.Errorf("row %d Go %v Source %s", i, failure, expected.Issues)
		}
	}
	for profile, review := range []bool{false, true} {
		for i, name := range names {
			def, _ := NativeBuiltinDefinition(name)
			host := Host{BaseURL: "http://127.0.0.1:1", TeamReviewRequired: review}
			session := NewSession()
			schemas, failure := session.RenderBatch([]Definition{def}, Options{Host: host, Model: "deepseek-flash"})
			if failure != nil {
				t.Fatal(failure)
			}
			expected := source.Profiles[profile][i]
			if !jsonSourcesEqual(raw(schemas[0]), expected.Schema) {
				t.Errorf("profile %d %s schema Go %s Source %s", profile, name, raw(schemas[0]), expected.Schema)
			}
			prompt, _ := session.OriginalPrompt(def, host)
			if !jsonSourcesEqual(prompt, array(expected.Prompt)) || !jsonSourcesEqual(def.Description, array(expected.Description)) || !jsonSourcesEqual(def.SearchHint, array(expected.Hint)) || def.ShouldDefer != expected.Deferred || def.AlwaysLoad != expected.Always {
				t.Errorf("profile %d %s prompt/metadata differ", profile, name)
			}
		}
	}
}
