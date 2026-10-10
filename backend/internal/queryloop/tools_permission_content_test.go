package queryloop

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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestQuestionPermissionContentMatchesActualToolExecutionSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	modelInput := protocol.NewJSONObject()
	_ = modelInput.UnmarshalJSON([]byte(`{"questions":[{"question":"Which?","header":"Choice","options":[{"label":"A","description":"a"},{"label":"B","description":"b"}],"multiSelect":false}],"answers":{"Which?":"A"}}`))
	data := protocol.NewJSONObject(protocol.JSONField{Name: "questions", Value: modelInput.Get("questions")}, protocol.JSONField{Name: "answers", Value: modelInput.Get("answers")})
	rawData, _ := data.MarshalJSON()
	image := json.RawMessage(`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jR1MAAAAASUVORK5CYII="}}`)
	history := []Message{{Record: protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("user")}, protocol.JSONField{Name: "imagePasteIds", Value: []byte(`[2,8]`)})}, {Record: protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("assistant")}, protocol.JSONField{Name: "imagePasteIds", Value: []byte(`[100]`)})}}
	permissions := []ToolPermission{{Behavior: "allow", Input: &modelInput}, {Behavior: "allow", Input: &modelInput, AcceptFeedback: quoted("user feedback"), ContentBlocks: []json.RawMessage{image, []byte(`{"type":"text","text":"context"}`), image}}, {Behavior: "ask", Input: &modelInput, Message: quoted("question clarification"), ContentBlocks: []json.RawMessage{image}}, {Behavior: "deny", Input: &modelInput, Message: quoted("rule denies question"), ContentBlocks: []json.RawMessage{image}}}
	permissions = append(permissions, ToolPermission{Behavior: "allow", Input: &modelInput, ContentBlocks: []json.RawMessage{image, image}})
	histories := make([][]Message, len(permissions))
	for i := range histories {
		histories[i] = history
	}
	fractional := history[0].Record.Clone()
	fractional.Set("imagePasteIds", []byte(`[0.5,4.25,4.5]`))
	histories[len(histories)-1] = []Message{{Record: fractional}, history[1]}
	cases := []any{}
	actual := []any{}
	for i, permission := range permissions {
		sourcePermission := map[string]any{"behavior": permission.Behavior, "updatedInput": modelInput}
		if permission.Message != nil {
			sourcePermission["message"] = permission.Message
		}
		if permission.AcceptFeedback != nil {
			sourcePermission["acceptFeedback"] = permission.AcceptFeedback
		}
		if len(permission.ContentBlocks) > 0 {
			sourcePermission["contentBlocks"] = permission.ContentBlocks
		}
		sourceHistory := []protocol.JSONObject{}
		for _, message := range histories[i] {
			sourceHistory = append(sourceHistory, message.Record)
		}
		cases = append(cases, map[string]any{"input": modelInput, "permission": sourcePermission, "history": sourceHistory})
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.Mkdir(config, 0700)
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-question-content.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_QUESTION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "cases": cases})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source permission content %v %.2400s", err, stderr.String())
	}
	var source struct {
		Values       []any
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(output, &source) != nil {
		t.Fatalf("Source JSON %.2000s", output)
	}
	if len(source.Capabilities.Blocked) > 0 {
		t.Fatal("fixture escaped", source.Capabilities.Blocked)
	}
	// The mapper is independently checked in builtintools/question_test.go.
	// Here its actual Source block is the input to the lower message-assembly
	// boundary, which must preserve it while adding permission content.
	for i, permission := range permissions {
		rows := source.Values[i].([]any)
		if len(rows) != 1 {
			t.Fatalf("Source user messages %d", len(rows))
		}
		contentRaw, _ := json.Marshal(rows[0].(map[string]any)["content"])
		var blocks []protocol.JSONObject
		if json.Unmarshal(contentRaw, &blocks) != nil || len(blocks) == 0 {
			t.Fatal("Source mapped tool result missing")
		}
		base := blocks[0]
		receipt := json.RawMessage(rawData)
		if permission.Behavior != "allow" {
			receipt, _ = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quoted("Error: "), permission.Message}, "")
		}
		options, err := permissionUserContent(base, permission, histories[i])
		if err != nil {
			t.Fatal(err)
		}
		row := map[string]any{"content": options["content"], "toolUseResult": receipt}
		if options["imagePasteIds"] != nil {
			row["imagePasteIds"] = options["imagePasteIds"]
		}
		actual = append(actual, []any{row})
	}
	var expected any
	raw, _ := json.Marshal(actual)
	_ = json.Unmarshal(raw, &expected)
	if !reflect.DeepEqual(expected, source.Values) {
		for i, value := range expected.([]any) {
			if !reflect.DeepEqual(value, source.Values[i]) {
				goRaw, _ := json.Marshal(value)
				sourceRaw, _ := json.Marshal(source.Values[i])
				t.Errorf("case%d Go=%s Source=%s", i, goRaw, sourceRaw)
			}
		}
	}
}

func TestQuestionPermissionContentSurvivesSerialAndConcurrentExecution(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		for _, behavior := range []string{"allow", "ask", "deny"} {
			t.Run(behavior+map[bool]string{false: "-serial", true: "-concurrent"}[concurrent], func(t *testing.T) {
				h := newBatchHarness(t)
				owner := cooperative.New()
				defer owner.Close()
				if concurrent {
					h.Tool.Name = "Read"
					h.Uses[0].Block.Object.Set("name", quoted("Read"))
					h.Tool.IsConcurrencySafe = func(protocol.JSONObject) bool { return true }
				}
				image := json.RawMessage(`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"cHJvY2Vzc2Vk"},"future":"kept"}`)
				h.Capabilities.ResolvePermission = func(_ context.Context, _ RegisteredTool, input *protocol.JSONObject, _ ToolCallContext) (ToolPermission, error) {
					return ToolPermission{Behavior: behavior, Input: input, Message: quoted("user clarification"), AcceptFeedback: quoted("approved feedback"), ContentBlocks: []json.RawMessage{image}}, nil
				}
				var emitted []Message
				err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					h.Capabilities.Owner, h.Capabilities.Scope = owner, scope
					if concurrent {
						h.Tool.StartCallJSON = func(context.Context, protocol.JSONObject, JSONToolCallContext) (*ownerasync.OwnerFuture[ToolCallResult], error) {
							h.Calls++
							future, err := ownerasync.NewOwnerFuture[ToolCallResult](owner)
							if err != nil {
								return nil, err
							}
							return future, future.Resolve(scope, ToolCallResult{Data: quoted("written")})
						}
					}
					batch, err := NewToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
					if err != nil {
						return err
					}
					history := []Message{{Record: fixtureObject(`{"type":"user","imagePasteIds":[3,9],"message":{"role":"user","content":"history"}}`)}}
					_, err = batch.Execute(context.Background(), Round{ToolContext: "original", History: history}, h.Uses, func(message Message) error {
						emitted = append(emitted, message)
						return nil
					})
					return err
				})
				if err != nil || len(emitted) != 1 {
					t.Fatalf("native permission execution %v emitted%d", err, len(emitted))
				}
				message := emitted[0]
				var api protocol.JSONObject
				_ = json.Unmarshal(message.Record.Get("message"), &api)
				var blocks []protocol.JSONObject
				_ = json.Unmarshal(api.Get("content"), &blocks)
				wantImages, wantFeedback := 1, 0
				if behavior == "deny" {
					wantImages = 0
				}
				if behavior == "allow" {
					wantFeedback = 1
				}
				images, feedback := 0, 0
				for _, block := range blocks {
					if block.String("type") == "image" {
						images++
						if !bytes.Equal(block.Get("future"), quoted("kept")) {
							t.Fatal("processed image fields changed")
						}
					}
					if block.String("type") == "text" && block.String("text") == "approved feedback" {
						feedback++
					}
				}
				if images != wantImages || feedback != wantFeedback || (wantImages == 1 && !bytes.Equal(message.Record.Get("imagePasteIds"), []byte(`[10]`))) {
					raw, _ := message.Record.MarshalJSON()
					t.Fatalf("permission content lost %s", raw)
				}
				if _, err := NormalizeOrdinaryToolResult(message); err != nil {
					t.Fatal("processed permission result rejected", err)
				}
				wantCalls := 0
				if behavior == "allow" {
					wantCalls = 1
				}
				if h.Calls != wantCalls {
					t.Fatal("tool executed without allow decision")
				}
			})
		}
	}
}
