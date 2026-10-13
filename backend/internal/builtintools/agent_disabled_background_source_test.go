package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func TestActualSourceAgentStartupBackgroundFlagKeepsParserAPISchemaAndBudgetTogether(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	for _, flag := range []string{"0", "true"} {
		t.Run(flag, func(t *testing.T) {
			s, owner, home, _ := fixture(t)
			s.options.Environment["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"] = flag
			s.options.RunAgent = func(context.Context, *cooperative.Scope, AgentRequest) (*ownerasync.OwnerFuture[AgentRunResult], error) {
				t.Fatal("schema inspection must not start an Agent")
				return nil, nil
			}
			fixturePath, _ := filepath.Abs("../../testdata/native-default-task-prompts.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixturePath)
			child.Dir = home
			child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=" + flag, "NODE_ENV=production", "CC_HAHA_DEFAULT_TASK_PROMPTS_FIXTURE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "PATH=/usr/bin:/bin"}
			child.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "rows": []any{}}))
			output, err := child.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(output))
			}
			var source struct {
				SwarmSchemas     [][]json.RawMessage
				LocalAgentSchema json.RawMessage
				AgentLocalParsed protocol.JSONObject
			}
			if err = json.Unmarshal(output, &source); err != nil || len(source.SwarmSchemas) != 5 {
				t.Fatal(err, string(output))
			}
			if err = owner.Run(ctx, func(scope *cooperative.Scope) error {
				defs, tools, err := s.Catalog(scope)
				if err != nil {
					return err
				}
				for i, def := range defs {
					if def.Name != "Agent" {
						continue
					}
					local, err := toolschema.OriginalInputJSONSchema(def)
					if err != nil || !jsonSame(local, source.LocalAgentSchema) {
						t.Fatal("Source local schema/budget differs", err, string(local), string(source.LocalAgentSchema))
					}
					parsed, err := tools[i].ParseInput(input(`{"description":"owned task","prompt":"read workspace","run_in_background":true,"name":"owned-name"}`))
					if err != nil || !jsonSame(raw(parsed), raw(source.AgentLocalParsed)) {
						t.Fatal("Source parsed fields differ", err, string(raw(parsed)), string(raw(source.AgentLocalParsed)))
					}
					host := s.SchemaHost("deepseek-flash", "http://127.0.0.1:1")
					rendered, err := toolschema.NewSession().RenderBatch([]toolschema.Definition{def}, toolschema.Options{Host: *host, Model: "deepseek-flash"})
					if err != nil || !jsonSame(rendered[0].Get("input_schema"), source.SwarmSchemas[0][0]) {
						t.Fatal("Source API background gate differs", err)
					}
					return nil
				}
				t.Fatal("real Agent not registered")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
