package requestcontext

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
	"testing"
)

func TestLocalCommandHistoryMergesOriginalOutputIntoNextRealModelUser(t *testing.T) {
	input := basicContext(t)
	var local protocol.JSONObject
	if e := json.Unmarshal([]byte(`{"type":"system","subtype":"local_command","content":"<local-command-stdout>13 input, 7 output</local-command-stdout>","uuid":"local-id","timestamp":"2023-11-14T22:13:20.000Z","isMeta":false}`), &local); e != nil {
		t.Fatal(e)
	}
	input.History = append([]protocol.JSONObject{local}, input.History...)
	before, _ := local.MarshalJSON()
	out, e := normalizeHistory(input.History, dependencies(), nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(out) != 1 || out[0].String("type") != "user" {
		t.Fatal(out)
	}
	data, _ := out[0].MarshalJSON()
	if !strings.Contains(string(data), "13 input, 7 output") || !strings.Contains(string(data), "question") || strings.Contains(string(data), `"local_command"`) {
		t.Fatal(string(data))
	}
	after, _ := local.MarshalJSON()
	if string(before) != string(after) {
		t.Fatal("normalizer mutated original persisted system message")
	}
}
