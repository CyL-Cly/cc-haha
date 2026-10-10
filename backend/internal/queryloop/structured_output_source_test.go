package queryloop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/jsonschemavalidator"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceStructuredOutputToolExecutionSerialAndConcurrent(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	keep := true
	drop := false
	rows := []struct {
		Schema   json.RawMessage `json:"schema"`
		Value    json.RawMessage `json:"value"`
		MapError bool            `json:"mapError,omitempty"`
		AgentID  string          `json:"agentId,omitempty"`
		Preserve *bool           `json:"preserveToolUseResults,omitempty"`
	}{
		{[]byte(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`), []byte(`{"answer":"真实𐐀"}`), false, "", nil},
		{[]byte(`{"type":"object","required":["answer"]}`), []byte(`{}`), false, "", nil},
		{[]byte(`{"type":"object"}`), []byte(`{"answer":2}`), true, "", nil},
		{[]byte(`{"type":"object"}`), []byte(`{}`), false, "", nil},
	}
	rows = append(rows,
		struct {
			Schema   json.RawMessage `json:"schema"`
			Value    json.RawMessage `json:"value"`
			MapError bool            `json:"mapError,omitempty"`
			AgentID  string          `json:"agentId,omitempty"`
			Preserve *bool           `json:"preserveToolUseResults,omitempty"`
		}{[]byte(`{"type":"object"}`), []byte(`{"answer":"child"}`), false, "actual-agent", &drop},
		struct {
			Schema   json.RawMessage `json:"schema"`
			Value    json.RawMessage `json:"value"`
			MapError bool            `json:"mapError,omitempty"`
			AgentID  string          `json:"agentId,omitempty"`
			Preserve *bool           `json:"preserveToolUseResults,omitempty"`
		}{[]byte(`{"type":"object"}`), []byte(`{"answer":"child-kept"}`), false, "actual-agent", &keep},
		struct {
			Schema   json.RawMessage `json:"schema"`
			Value    json.RawMessage `json:"value"`
			MapError bool            `json:"mapError,omitempty"`
			AgentID  string          `json:"agentId,omitempty"`
			Preserve *bool           `json:"preserveToolUseResults,omitempty"`
		}{[]byte(`{"type":"object","required":["answer"]}`), []byte(`{}`), false, "actual-agent", &drop},
	)
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
	data, _ := json.Marshal(map[string]any{"homeDir": home, "structuredExecutions": rows})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_WORKFLOW_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if e := command.Run(); e != nil {
		t.Fatalf("actual Source executor %v %.1800s %.1800s", e, stdout.String(), stderr.String())
	}
	var source struct {
		StructuredExecutions [][]protocol.JSONObject
		Capabilities         struct{ Blocked []string }
	}
	if e := json.Unmarshal(stdout.Bytes(), &source); e != nil || len(source.StructuredExecutions) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, stdout.String(), stderr.String())
	}
	compiler, e := jsonschemavalidator.New()
	if e != nil {
		t.Fatal(e)
	}
	for index, row := range rows {
		validator, e := compiler.Compile(ctx, uint64(index+1), row.Schema)
		if e != nil {
			t.Fatal(e)
		}
		for _, concurrent := range []bool{false, true} {
			t.Run(string(rune('a'+index))+map[bool]string{false: "-serial", true: "-concurrent"}[concurrent], func(t *testing.T) {
				h := newBatchHarness(t)
				h.Capabilities.OmitSuccessfulToolUseResult = row.AgentID != "" && row.Preserve != nil && !*row.Preserve
				owner := cooperative.New()
				defer owner.Close()
				h.Tool.Name = "StructuredOutput"
				h.Tool.IsConcurrencySafe = func(protocol.JSONObject) bool { return concurrent }
				h.Uses[0].Block.Object.Set("name", quoted(h.Tool.Name))
				h.Uses[0].Block.Object.Set("id", quoted("structured-call"))
				h.Uses[0].Block.Object.Set("input", row.Value)
				call := func(_ context.Context, in protocol.JSONObject, _ ToolCallContext) (ToolCallResult, error) {
					if e := validator.Validate(ctx, in); e != nil {
						return ToolCallResult{}, e
					}
					return ToolCallResult{Data: quoted("Structured output provided successfully"), StructuredOutput: row.Value}, nil
				}
				h.Tool.Call = call
				h.Tool.StartCallJSON = func(_ context.Context, in protocol.JSONObject, _ JSONToolCallContext) (*ownerasync.OwnerFuture[ToolCallResult], error) {
					future, e := ownerasync.NewOwnerFuture[ToolCallResult](owner)
					if e != nil {
						return nil, e
					}
					go func() {
						result, cause := call(ctx, in, ToolCallContext{})
						receipt, e := owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
							if cause != nil {
								return future.Reject(scope, cause)
							}
							return future.Resolve(scope, result)
						})
						if e == nil {
							e = receipt.Wait()
						}
						if e != nil {
							_ = future.RejectAdministrative(e)
						}
					}()
					return future, nil
				}
				h.Tool.MapResult = func(raw json.RawMessage, id string) (protocol.JSONObject, error) {
					if row.MapError {
						return protocol.JSONObject{}, errors.New("actual mapper failed")
					}
					return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: quoted(id)}, protocol.JSONField{Name: "type", Value: quoted("tool_result")}, protocol.JSONField{Name: "content", Value: raw}), nil
				}
				out := []protocol.JSONObject{}
				e = owner.Run(ctx, func(scope *cooperative.Scope) error {
					h.Capabilities.Owner, h.Capabilities.Scope = owner, scope
					batch, e := NewToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
					if e != nil {
						return e
					}
					_, e = batch.Execute(ctx, Round{ToolContext: "original"}, h.Uses, func(message Message) error {
						record := message.Record.Clone()
						record.Delete("uuid")
						record.Delete("timestamp")
						record.Delete("sourceToolAssistantUUID")
						out = append(out, record)
						return nil
					})
					return e
				})
				if e != nil {
					t.Fatal(e)
				}
				canonical := func(value any) any {
					raw, _ := json.Marshal(value)
					var decoded any
					_ = json.Unmarshal(raw, &decoded)
					return decoded
				}
				if !reflect.DeepEqual(canonical(out), canonical(source.StructuredExecutions[index])) {
					actual, _ := json.Marshal(out)
					expected, _ := json.Marshal(source.StructuredExecutions[index])
					t.Fatalf("actual Source structured sequence differs\nGo%s\nSource%s", actual, expected)
				}
				// Attachment allocation precedes feedback, even if the mapper
				// later fails and the Source catch replaces the returned list.
				wantIDs := 2
				if index == 1 || index == 6 {
					wantIDs = 1
				}
				if h.UUIDs != wantIDs {
					t.Fatal("Source allocation stage changed", h.UUIDs, wantIDs)
				}
			})
		}
	}
}

func TestStructuredAttachmentConsumerFailureStopsFeedback(t *testing.T) {
	h := newBatchHarness(t)
	stop := errors.New("attachment consumer stopped")
	h.Tool.Call = func(context.Context, protocol.JSONObject, ToolCallContext) (ToolCallResult, error) {
		return ToolCallResult{Data: quoted("ok"), StructuredOutput: []byte(`null`)}, nil
	}
	delivered := 0
	h.Emit = func(message Message) error {
		delivered++
		if message.Type() != "attachment" {
			t.Fatal("feedback before structured attachment")
		}
		return stop
	}
	if _, e := h.run(); e != stop || delivered != 1 {
		t.Fatal(e, delivered)
	}
}
