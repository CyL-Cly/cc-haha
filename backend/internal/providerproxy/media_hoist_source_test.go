package providerproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNestedMediaCompatibilityPreservesActualSourceOrderProvenanceAndCache(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	type scenario struct {
		Kind  string              `json:"kind"`
		Value protocol.JSONObject `json:"value"`
	}
	rows := []scenario{}
	for _, format := range []string{"anthropic", "openai_chat", "openai_responses"} {
		for _, resolved := range []string{"anthropic", "openai_chat", "openai_responses"} {
			for _, flag := range []string{"false", "true", "null", "0", `"false"`} {
				rows = append(rows, scenario{"predicate", requestRecord(requestField("providerApiFormat", format), requestField("resolvedApiFormat", resolved), protocol.JSONField{Name: "supportsNestedToolResultMedia", Value: []byte(flag)})})
			}
		}
	}
	image := `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"},"cache_control":{"type":"ephemeral"}}`
	text := `{"type":"text","text":"one\ud800","cache_control":null,"citations":[{"future":true}]}`
	documents := []string{
		`{"type":"document","source":{"type":"text","data":"body\udc00"}}`,
		`{"type":"document","title":"Title\ud800","context":"Context\udc00","cache_control":{"type":"ephemeral"},"source":{"type":"text","data":"body"}}`,
		`{"type":"document","title":"Title","context":"Context","cache_control":null,"source":{"type":"content","content":"text"}}`,
		`{"type":"document","title":"Title","cache_control":{"type":"ephemeral"},"source":{"type":"content","content":[]}}`,
		`{"type":"document","cache_control":{"type":"ephemeral"},"source":{"type":"content","content":[]}}`,
		`{"type":"document","title":"Title","context":"Context","cache_control":{"type":"ephemeral"},"source":{"type":"content","content":[` + text + `,{"type":"text","text":"two"}]}}`,
		`{"type":"document","cache_control":{"type":"ephemeral"},"source":{"type":"content","content":[` + text + `]}}`,
		`{"type":"document","source":{"type":"content","content":[` + text + `,` + image + `]}}`,
		`{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"AAAA"}}`,
		`{"type":"document","source":{"type":"url","url":"https://private.invalid/fictional-document"}}`,
	}
	for _, doc := range documents {
		for _, inner := range []string{doc, text + "," + doc + "," + image, image + "," + doc + "," + image} {
			for _, previous := range []string{
				`{"role":"assistant","content":[{"type":"tool_use","id":"Read\ud800","name":"Read","input":{}}]}`,
				`{"role":"assistant","content":[{"type":"server_tool_use","id":"srv"}]}`,
				`{"role":"assistant","content":[{"type":"server_tool_use","id":"srv"},{"type":"web_search_tool_result","tool_use_id":"srv"}]}`,
				`{"role":"assistant","content":[{"type":"mcp_tool_use","id":"mcp"}]},{"role":"user","content":"intermediate"}`,
				`{"role":"assistant","content":[{"type":"mcp_tool_use","id":"mcp"}]},{"role":"assistant","content":"resolved next turn"}`,
			} {
				body := requestObject([]byte(`{"model":"fixture","future":{"keep":true},"tools":[{"type":"web_search_20250305"}],"messages":[` + previous + `,{"role":"user","future":"keep","content":[{"type":"text","text":"leading"},{"type":"tool_result","tool_use_id":"Read\ud800","is_error":false,"future":true,"content":[` + inner + `]},{"type":"tool_result","tool_use_id":"second","content":[` + image + `]},{"type":"text","text":"trailing"}]}]}`))
				rows = append(rows, scenario{"hoist", body})
			}
		}
	}
	for _, messages := range []string{`[]`, `[{"role":"user","content":"plain"}]`, `[{"role":"assistant","content":[` + image + `]}]`, `[{"role":"user","content":[{"type":"tool_result","tool_use_id":"R","content":"text"}]}]`, `[{"role":"user","content":[{"type":"tool_result","tool_use_id":"R","content":null}]}]`} {
		rows = append(rows, scenario{"hoist", requestObject([]byte(`{"messages":` + messages + `}`))})
	}
	for _, body := range []string{
		`{}`, `{"messages":null}`, `{"messages":false}`, `{"messages":0}`, `{"messages":"text"}`, `{"messages":{}}`,
		`{"messages":[null]}`, `{"messages":[false]}`, `{"messages":[{"role":"user"}]}`, `{"messages":[{"role":"user","content":null}]}`, `{"messages":[{"role":"user","content":false}]}`, `{"messages":[{"role":"user","content":{}}]}`,
		`{"messages":[{"role":"assistant"},{"role":"user","content":[]}]}`, `{"messages":[{"role":"assistant","content":null},{"role":"user","content":[]}]}`, `{"messages":[{"role":"assistant","content":false},{"role":"user","content":[]}]}`, `{"messages":[{"role":"assistant","content":[null]},{"role":"user","content":[]}]}`,
		`{"messages":[{"role":"user","content":[null]}]}`, `{"messages":[{"role":"user","content":[{"type":"tool_result","content":[null]}]}]}`,
		`{"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"document"}]}]}]}`, `{"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"document","source":null}]}]}]}`,
		`{"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"document","source":{"type":"content"}}]}]}]}`, `{"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"document","source":{"type":"content","content":null}}]}]}]}`, `{"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"document","source":{"type":"content","content":false}}]}]}]}`, `{"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"document","source":{"type":"content","content":[null]}}]}]}]}`,
	} {
		rows = append(rows, scenario{"hoist", requestObject([]byte(body))})
	}
	for _, id := range []string{"", `,"tool_use_id":null`, `,"tool_use_id":0`, `,"tool_use_id":{}`, `,"tool_use_id":["x",null,"\ud800"]`} {
		rows = append(rows, scenario{"hoist", requestObject([]byte(`{"messages":[{"role":"user","content":[{"type":"tool_result"` + id + `,"content":[` + image + `]}]}]}`))})
	}
	for _, data := range []string{"", `,"data":null`, `,"data":false`, `,"data":0`, `,"data":{}`, `,"data":["x",null,"\ud800"]`} {
		rows = append(rows, scenario{"hoist", requestObject([]byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"document","source":{"type":"text"` + data + `}}]}]}]}`))})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-providerproxy-media-hoist.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(requestEncode(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual media Source %v %s", err, stderr.String())
	}
	var expected []json.RawMessage
	if json.Unmarshal(data, &expected) != nil || len(expected) != len(rows) {
		t.Fatal("unexpected actual Source shape", string(data))
	}
	for index, row := range rows {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			before := requestEncode(row.Value)
			var actual json.RawMessage
			if row.Kind == "predicate" {
				actual = requestEncode(ShouldHoistNestedToolResultMedia(row.Value))
			} else {
				value, err := HoistToolResultMediaForCompatibility(row.Value)
				if err != nil {
					name := "Error"
					if failure, ok := err.(*ResponseError); ok {
						name = failure.ErrorName()
					}
					actual = requestEncode(requestRecord(requestField("error", requestRecord(requestField("name", name), requestField("message", err.Error())))))
				} else {
					actual = requestEncode(value)
				}
			}
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(actual), protocol.StringifyValidatedJavaScriptJSON(expected[index])) {
				t.Fatalf("Source %s Go %s", expected[index], actual)
			}
			if !bytes.Equal(before, requestEncode(row.Value)) {
				t.Fatal("compatibility rewrite mutated original history")
			}
		})
	}
}
