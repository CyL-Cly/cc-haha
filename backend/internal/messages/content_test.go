package messages

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func sameJSON(t *testing.T, want, actual json.RawMessage, path string) {
	t.Helper()
	want, actual = bytes.TrimSpace(want), bytes.TrimSpace(actual)
	if len(want) == 0 || len(actual) == 0 {
		t.Fatalf("%s missing JSON: want=%s actual=%s", path, want, actual)
	}
	if want[0] != actual[0] {
		t.Fatalf("%s value mismatch: want=%s actual=%s", path, want, actual)
	}
	switch want[0] {
	case '{':
		var left, right storage.Object
		if json.Unmarshal(want, &left) != nil || json.Unmarshal(actual, &right) != nil || len(left) != len(right) {
			t.Fatalf("%s object mismatch: want=%s actual=%s", path, want, actual)
		}
		for field, value := range left {
			sameJSON(t, value, right[field], path+"."+field)
		}
	case '[':
		var left, right []json.RawMessage
		if json.Unmarshal(want, &left) != nil || json.Unmarshal(actual, &right) != nil || len(left) != len(right) {
			t.Fatalf("%s array mismatch: want=%s actual=%s", path, want, actual)
		}
		for index, value := range left {
			sameJSON(t, value, right[index], path+"[]")
		}
	case '"':
		left, errLeft := protocol.DecodeJavaScriptJSONString(want)
		right, errRight := protocol.DecodeJavaScriptJSONString(actual)
		if errLeft != nil || errRight != nil || !reflect.DeepEqual(left, right) {
			t.Fatalf("%s UTF16 string mismatch: want=%s actual=%s", path, want, actual)
		}
	default:
		if !bytes.Equal(want, actual) {
			t.Fatalf("%s primitive mismatch: want=%s actual=%s", path, want, actual)
		}
	}
}

func TestContentMergingMatchesFrozenActualTypeScriptContracts(t *testing.T) {
	raw, err := os.ReadFile("testdata/content_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		SourceCommit string `json:"sourceCommit"`
		Cases        []struct {
			Name  string `json:"name"`
			Input struct {
				Operation string         `json:"operation"`
				A         storage.Object `json:"a"`
				B         storage.Object `json:"b"`
				BlocksA   []ContentBlock `json:"blocksA"`
				BlocksB   []ContentBlock `json:"blocksB"`
				Universal bool           `json:"universal"`
			} `json:"input"`
			Expected json.RawMessage `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.SourceCommit != "6b9d2aec1d9321a4dfff90d16b51f10c1ab984c8" || len(contract.Cases) < 25 {
		t.Fatal("missing actual TS contract provenance")
	}
	for _, row := range contract.Cases {
		t.Run(row.Name, func(t *testing.T) {
			step := row.Input
			var value any
			var err error
			switch step.Operation {
			case "merge-content":
				var merged []ContentBlock
				merged, err = MergeUserContentBlocks(step.BlocksA, step.BlocksB, step.Universal)
				value = map[string]any{"merged": merged, "blocksAAfter": step.BlocksA, "blocksBAfter": step.BlocksB}
			case "reorder-tools":
				value = map[string]any{"merged": ReorderAssistantToolUseBlocks(step.BlocksA), "blocksAAfter": step.BlocksA}
			case "merge-user", "merge-user-tools", "merge-assistant":
				var merged storage.Object
				switch step.Operation {
				case "merge-user":
					merged, err = MergeUserMessages(step.A, step.B)
				case "merge-user-tools":
					merged, err = MergeUserMessagesAndToolResults(step.A, step.B, step.Universal)
				case "merge-assistant":
					merged, err = MergeAssistantMessages(step.A, step.B)
				}
				value = map[string]any{"merged": merged, "aAfter": step.A, "bAfter": step.B}
			default:
				t.Fatal("unknown actual TS contract operation", step.Operation)
			}
			if err != nil {
				t.Fatal(err)
			}
			actual, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			actual, err = protocol.ParseAndStringifyJavaScriptJSON(actual)
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, row.Expected, actual, "$")
			var wantResult, gotResult storage.Object
			_ = json.Unmarshal(row.Expected, &wantResult)
			_ = json.Unmarshal(actual, &gotResult)
			wantContent, gotContent := wantResult["merged"], gotResult["merged"]
			if step.Operation != "merge-content" && step.Operation != "reorder-tools" {
				var wantEnvelope, gotEnvelope, wantAPI, gotAPI storage.Object
				_ = json.Unmarshal(wantContent, &wantEnvelope)
				_ = json.Unmarshal(gotContent, &gotEnvelope)
				_ = json.Unmarshal(wantEnvelope["message"], &wantAPI)
				_ = json.Unmarshal(gotEnvelope["message"], &gotAPI)
				wantContent, gotContent = wantAPI["content"], gotAPI["content"]
			}
			wantContent, _ = protocol.ParseAndStringifyJavaScriptJSON(wantContent)
			gotContent, _ = protocol.ParseAndStringifyJavaScriptJSON(gotContent)
			if !bytes.Equal(wantContent, gotContent) {
				t.Fatalf("model-visible content bytes differ: want=%s actual=%s", wantContent, gotContent)
			}
		})
	}
}

func TestContentMergeRejectsMalformedHostInputsWithoutMutation(t *testing.T) {
	valid := options(`{"type":"user","message":{"role":"user","content":"hello"},"uuid":"first"}`)
	before := objectJSON(valid)
	for _, malformed := range []string{`{}`, `{"message":null}`, `{"message":{"content":null}}`, `{"message":{"content":1}}`} {
		if _, err := MergeUserMessages(options(malformed), valid); err == nil {
			t.Fatal("malformed input accepted", malformed)
		}
		if _, err := MergeUserMessages(valid, options(malformed)); err == nil {
			t.Fatal("malformed next message accepted", malformed)
		}
	}
	if !bytes.Equal(before, objectJSON(valid)) {
		t.Fatal("failed merge mutated history")
	}
	for _, raw := range []string{`{"type":"tool_result","content":null}`, `{"type":"tool_result","content":42}`, `{"type":"tool_result","content":[{"type":"text","text":null}]}`} {
		if _, err := MergeUserContentBlocks(blockOptions(raw), blockOptions(`{"type":"text","text":"after"}`), true); err == nil {
			t.Fatal("malformed tool content accepted", raw)
		}
	}
	for _, universal := range []bool{false, true} {
		if _, err := MergeUserContentBlocks(blockOptions(`{"type":"tool_result","content":"before"}`), blockOptions(`{"type":"text","text":null}`), universal); err == nil {
			t.Fatal("malformed appended text accepted")
		}
	}
}

func blockOptions(raw string) []ContentBlock {
	var block ContentBlock
	if err := json.Unmarshal([]byte(raw), &block); err != nil {
		panic(err)
	}
	return []ContentBlock{block}
}

func TestMetaMergeWithMissingNextUUIDPreservesMissingVersusNull(t *testing.T) {
	a := options(`{"type":"user","uuid":"meta","isMeta":true,"message":{"role":"user","content":"before"}}`)
	merged, err := MergeUserMessages(a, options(`{"type":"user","message":{"role":"user","content":[]}}`))
	if err != nil || merged["uuid"] != nil {
		t.Fatal(merged, err)
	}
	if string(a["uuid"]) != `"meta"` {
		t.Fatal("original UUID mutated")
	}
}
