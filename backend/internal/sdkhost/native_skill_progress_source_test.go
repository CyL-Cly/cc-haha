package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkoutput"
)

func TestNativeForkSkillProgressUsesActualSourceSplitNormalizerAndOwnerBinding(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	assistant := object([]byte(`{"type":"assistant","uuid":"11111111-1111-4111-8111-111111111111","timestamp":"2026-10-16T00:00:00.000Z","message":{"id":"child-message","type":"message","role":"assistant","model":"fixture-model","content":[{"type":"tool_use","id":"child-write","name":"Write","input":{"file_path":"owned.txt","content":"private"}}],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}}`))
	user := object([]byte(`{"type":"user","uuid":"22222222-2222-4222-8222-222222222222","timestamp":"2026-10-16T00:00:01.000Z","toolUseResult":{"completed":true},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"child-write","content":"original child result"}]}}`))
	// Freeze the single-block cases before mutating shallow-cloned nested objects.
	singleAssistant := string(raw(assistant))
	singleUser := string(raw(user))
	multiAssistant := assistant.Clone()
	api, _ := multiAssistant.Object("message")
	api.Set("content", raw([]map[string]any{{"type": "text", "text": "leading text"}, {"type": "tool_use", "id": "child-write", "name": "Write", "input": map[string]any{"file_path": "owned.txt", "content": "private"}}, {"type": "text", "text": "between tools"}, {"type": "tool_use", "id": "child-read", "name": "Read", "input": map[string]any{"file_path": "owned.txt"}}}))
	multiAssistant.Set("message", raw(api))
	multiUser := user.Clone()
	api, _ = multiUser.Object("message")
	api.Set("content", raw([]map[string]any{{"type": "tool_result", "tool_use_id": "child-write", "content": "write done"}, {"type": "text", "text": "between results"}, {"type": "tool_result", "tool_use_id": "child-read", "content": "read done"}}))
	multiUser.Set("message", raw(api))
	originals := []string{singleAssistant, singleUser, string(raw(multiAssistant)), string(raw(multiUser))}
	expectedShapes := [][]string{{"tool_use"}, {"tool_result"}, {"text", "tool_use", "text", "tool_use"}, {"tool_result", "text", "tool_result"}}
	inputShapes := make([][]string, len(originals))
	for i, original := range originals {
		var input struct {
			Type    string
			UUID    string
			Message struct{ Content []struct{ Type string } }
		}
		if err := json.Unmarshal([]byte(original), &input); err != nil {
			t.Fatal(err)
		}
		expectedType, expectedUUID := "assistant", "11111111-1111-4111-8111-111111111111"
		if i%2 == 1 {
			expectedType, expectedUUID = "user", "22222222-2222-4222-8222-222222222222"
		}
		for _, block := range input.Message.Content {
			inputShapes[i] = append(inputShapes[i], block.Type)
		}
		if input.Type != expectedType || input.UUID != expectedUUID || !reflect.DeepEqual(inputShapes[i], expectedShapes[i]) {
			t.Fatalf("independent Skill input case%d type=%s uuid=%s shape=%v expected=%v", i, input.Type, input.UUID, inputShapes[i], expectedShapes[i])
		}
	}
	t.Logf("SKILL_PROGRESS_INPUT_SHAPES %s", raw(inputShapes))
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(temp, 0700); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-skill-progress.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CLAUDE_CODE_TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SKILL_PROGRESS_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=fake-skill-progress-key", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	payload, err := json.Marshal(map[string]any{"homeDir": home, "messages": originals})
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source Skill normalizer: %v %.8000s", err, output)
	}
	var source struct {
		Cases []struct {
			Normalized, Progress, SDK []json.RawMessage
			Transcript, Chain         []bool
		}
		Capabilities struct{ Blocked []string }
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Cases) != len(originals) || len(source.Capabilities.Blocked) != 0 {
		t.Fatalf("Source Skill shape: %v %.8000s", err, output)
	}
	factory, err := messages.NewFactory(func() string { return "fixture" }, func() time.Time { return time.Unix(0, 0) })
	if err != nil {
		t.Fatal(err)
	}
	normalizer, err := sdkoutput.New(sdkoutput.Dependencies{Factory: factory, SessionID: func() json.RawMessage { return quote("11111111-1111-4111-8111-111111111111") }, NowMillis: func() float64 { return 0 }, OriginalEnv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	host := &Host{options: Options{Owner: owner, Print: &PrintOptions{NativeProcess: true}}, normalizer: normalizer}
	if _, err := host.NativeSkillProgressMessages(nil, []byte(originals[0])); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("nil normalization scope", err)
	}
	if err := foreign.Run(ctx, func(scope *cooperative.Scope) error {
		_, err := host.NativeSkillProgressMessages(scope, []byte(originals[0]))
		if !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign normalization scope: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var retained *cooperative.Scope
	if err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		retained = scope
		for i, original := range originals {
			normalized, err := host.NativeSkillProgressMessages(scope, []byte(original))
			if err != nil {
				return err
			}
			var got, want any
			if err := json.Unmarshal(raw(normalized), &got); err != nil {
				return err
			}
			if err := json.Unmarshal(raw(source.Cases[i].Normalized), &want); err != nil {
				return err
			}
			if !reflect.DeepEqual(got, want) {
				return fmt.Errorf("complete Source split/UUID mismatch case%d Go=%s Source=%s", i, raw(normalized), raw(source.Cases[i].Normalized))
			}
			sdk := []json.RawMessage{}
			for _, record := range normalized {
				api, _ := record.Object("message")
				var blocks []protocol.JSONObject
				if err := json.Unmarshal(api.Get("content"), &blocks); err != nil {
					return err
				}
				if len(blocks) != 1 {
					return fmt.Errorf("normalizer retained multiple blocks: %s", raw(record))
				}
				if kind := blocks[0].String("type"); kind != "tool_use" && kind != "tool_result" {
					continue
				}
				progress := factory.CreateProgress(quote("skill_parent-message"), quote("parent-skill"), protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("skill_progress")}, protocol.JSONField{Name: "message", Value: raw(record)}, protocol.JSONField{Name: "prompt", Value: quote("original skill prompt")}, protocol.JSONField{Name: "agentId", Value: quote("child-agent")}))
				if !retainedNativeSkillProgress(progress) {
					return fmt.Errorf("actual Skill progress rejected: %s", raw(progress))
				}
				decoded, err := sdkoutput.Decode(raw(progress))
				if err != nil {
					return err
				}
				iterator := normalizer.IterateMessage(decoded)
				for {
					view, done, err := iterator.Next()
					if err != nil {
						return err
					}
					if done {
						break
					}
					encoded, err := view.MarshalJSON()
					if err != nil {
						return err
					}
					sdk = append(sdk, encoded)
				}
			}
			if err := json.Unmarshal(raw(sdk), &got); err != nil {
				return err
			}
			if err := json.Unmarshal(raw(source.Cases[i].SDK), &want); err != nil {
				return err
			}
			if !reflect.DeepEqual(got, want) {
				return fmt.Errorf("complete Skill SDK frame mismatch case%d Go=%s Source=%s", i, raw(sdk), raw(source.Cases[i].SDK))
			}
			expected := 1
			if i >= 2 {
				expected = 2
			}
			if len(sdk) != expected || len(source.Cases[i].Progress) != expected || len(source.Cases[i].Transcript) != expected || len(source.Cases[i].Chain) != expected {
				return fmt.Errorf("Source tool-only selection count case%d", i)
			}
			for j := range source.Cases[i].Progress {
				if source.Cases[i].Transcript[j] || source.Cases[i].Chain[j] {
					return fmt.Errorf("Source progress unexpectedly entered persisted chain")
				}
			}
		}
		host.closing = true
		_, err := host.NativeSkillProgressMessages(scope, []byte(originals[0]))
		if !errors.Is(err, cooperative.ErrClosed) {
			return fmt.Errorf("closed host normalized: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := host.NativeSkillProgressMessages(retained, []byte(originals[0])); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("retained normalization scope", err)
	}
	valid := object(source.Cases[0].Progress[0])
	for _, change := range []struct {
		key   string
		value json.RawMessage
		top   bool
	}{{"type", quote("agent_progress"), false}, {"type", quote("unknown_progress"), false}, {"agentId", raw(nil), false}, {"prompt", raw(7), false}, {"extra", raw(true), false}, {"message", raw(map[string]any{"type": "system"}), false}, {"toolUseID", quote("agent-progress"), true}, {"toolUseID", quote("skill_"), true}, {"parentToolUseID", raw(nil), true}} {
		invalid := valid.Clone()
		if change.top {
			invalid.Set(change.key, change.value)
		} else {
			data, _ := invalid.Object("data")
			data.Set(change.key, change.value)
			invalid.Set("data", raw(data))
		}
		if retainedNativeSkillProgress(invalid) {
			t.Fatalf("foreign progress admitted: %s", raw(invalid))
		}
	}
	t.Logf("SKILL_PROGRESS_SOURCE_CONTRACT %s", output)
}
