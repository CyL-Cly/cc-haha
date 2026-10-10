package attachments_test

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceRetainedSkillsContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/retained_skills_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Source string
		Cases  []struct {
			Input    originalCase
			Expected observation
		}
	}
	if err = json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if len(contract.Cases) == 0 || contract.Source != "actual exported Source normalizers + pairing, Bun 1.3.14" {
		t.Fatal("missing trustworthy actual Source contract")
	}
	for _, item := range contract.Cases {
		t.Run(item.Input.Name, func(t *testing.T) {
			actual := runCase(item.Input)
			if !reflect.DeepEqual(actual, item.Expected) {
				a, _ := json.Marshal(actual)
				b, _ := json.Marshal(item.Expected)
				t.Fatalf("actual %s\nexpected %s", a, b)
			}
		})
	}
}
func record(raw string) protocol.JSONObject {
	var value protocol.JSONObject
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		panic(err)
	}
	return value
}
func TestCapabilitiesCannotBeDroppedAsMalformed(t *testing.T) {
	factory, _ := messages.NewFactory(func() string { t.Fatal("unexpected UUID"); return "" }, func() time.Time { t.Fatal("unexpected clock"); return time.Time{} })
	for _, raw := range []string{`{"type":"file","content":"protected"}`, `{"type":"invoked_skills","skills":{"map":1}}`, `null`, `false`, `{invalid`} {
		_, err := attachments.Project(json.RawMessage(raw), factory)
		var cap *attachments.CapabilityError
		var malformed *attachments.MalformedAttachmentError
		if !errors.As(err, &cap) || errors.As(err, &malformed) {
			t.Fatalf("unsupported %s must fail closed distinctly, got %v", raw, err)
		}
		if cap.Error() == "" || !strings.Contains(cap.Error(), "attachment capability is not migrated:") {
			t.Fatal("missing explicit capability diagnostic")
		}
	}
	if _, err := attachments.Project(json.RawMessage(`{"type":"skill_listing","content":"real"}`), nil); err == nil {
		t.Fatal("nonempty projection requires real factory")
	}
}
func TestAttachmentMergePreservesUnknownFieldsAndOriginalInputs(t *testing.T) {
	a := record(`{"future":"left","type":"user","uuid":"left-id","message":{"future":3,"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"old"}]},"timestamp":"original"}`)
	b := record(`{"type":"user","message":{"role":"user","content":"<system-reminder>\nnew\n</system-reminder>"},"isMeta":true,"uuid":"discarded","timestamp":"new"}`)
	beforeA, _ := a.MarshalJSON()
	beforeB, _ := b.MarshalJSON()
	merged, err := attachments.MergeIntoUser(a, b)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := merged.MarshalJSON()
	wanted := `{"future":"left","type":"user","uuid":"left-id","message":{"future":3,"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"old\n\n<system-reminder>\nnew\n</system-reminder>"}]},"timestamp":"original"}`
	if js(raw) != wanted {
		t.Fatalf("ordered retained content %s", js(raw))
	}
	afterA, _ := a.MarshalJSON()
	afterB, _ := b.MarshalJSON()
	if string(beforeA) != string(afterA) || string(beforeB) != string(afterB) {
		t.Fatal("merge mutated original input")
	}
}
func TestMergeRejectsMalformedOrUnserializableRecords(t *testing.T) {
	valid := record(`{"type":"user","message":{"role":"user","content":"ok"}}`)
	broken := protocol.NewJSONObject(protocol.JSONField{Name: "broken", Value: json.RawMessage("invalid")})
	for _, pair := range [][2]protocol.JSONObject{{broken, valid}, {valid, broken}, {record(`{"message":null}`), valid}, {valid, record(`{"message":{"content":null}}`)}} {
		if _, err := attachments.MergeIntoUser(pair[0], pair[1]); err == nil {
			t.Fatal("malformed merge must fail")
		}
	}
}
func TestEmptyReorderDoesNotMutateInput(t *testing.T) {
	if result := attachments.ReorderForAPI(nil); len(result) != 0 {
		t.Fatal(result)
	}
	input := []protocol.JSONObject{record(`{"type":"attachment","attachment":{"type":"skill_listing","content":"x"}}`), record(`{"type":"user","message":null}`)}
	before, _ := json.Marshal(input)
	_ = attachments.ReorderForAPI(input)
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Fatal("reorder mutated input")
	}
}
