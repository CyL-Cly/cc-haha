package providerproxy

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestChatRequestAgainstActualSourceMediaBoundariesReasoningAndCompatibility(t *testing.T) {
	base := requestObject([]byte(`{"model":"gpt-6-sol","messages":[{"role":"user","content":"hello"}],"max_tokens":16,"stream":true}`))
	rows := []chatSourceScenario{}
	contents := []string{`"plain\ud800"`, `[]`, `null`, `false`, `[{"type":"text","text":"a"},{"type":"text","text":"b"}]`, `[{"type":"thinking","thinking":"thought\udc00"},{"type":"text","text":"answer"}]`, `[{"type":"tool_use","id":"call","name":"Read","input":{"path":"x\ud800","z":2,"a":1}}]`, `[{"type":"tool_use","id":"call","name":"Read","input":"{raw}"}]`, `[{"type":"tool_use","id":"call","name":"Read"}]`, `[{"type":"unknown","future":true}]`, `[{"type":"search_result","title":"Title","source":"https://fixture.invalid/source","content":[{"type":"text","text":"one"},{"type":"text","text":"two"}]}]`, `[{"type":"tool_result","tool_use_id":"R","content":"text"}]`, `[{"type":"tool_result","tool_use_id":"R","content":[]}]`, `[{"type":"tool_result","tool_use_id":"R"}]`, `[{"type":"tool_result","tool_use_id":"R","content":null}]`}
	image := `{"type":"image","source":{"type":"url","url":"https://fixture.invalid/image"}}`
	media := []string{image, `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}`, `{"type":"image","source":{"type":"file","file_id":"fixture"}}`, `{"type":"document","title":"Doc","context":"Context","source":{"type":"text","data":"body\ud800"}}`, `{"type":"document","title":"Doc","context":"Context","source":{"type":"url","url":"https://fixture.invalid/document"}}`, `{"type":"document","title":"","context":"Context","source":{"type":"file","file_id":"fixture"}}`, `{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"AAAA"}}`, `{"type":"document","title":"Doc","source":{"type":"content","content":"text"}}`, `{"type":"document","title":"Doc","source":{"type":"content","content":[{"type":"text","text":"one"},` + image + `,{"type":"text","text":"two"}]}}`, `{"type":"document","source":{"type":"content","content":[{"type":"text","text":"one"},{"type":"image","source":{"type":"file","file_id":"f"}},{"type":"text","text":"two"}]}}`}
	for _, text := range []string{"\ufeffA💫B", strings.Repeat("x", 1999) + "💫finish", "before\r\nafter"} {
		encoded := base64.StdEncoding.EncodeToString([]byte(text))
		media = append(media, `{"type":"document","context":"Context","source":{"type":"base64","media_type":"text/plain","data":`+string(requestEncode(encoded))+`}}`)
	}
	for _, part := range media {
		contents = append(contents, `[`+part+`]`, `[{"type":"text","text":"before"},`+part+`,{"type":"text","text":"after"}]`, `[{"type":"tool_result","tool_use_id":"R","content":[{"type":"text","text":"before"},`+part+`,{"type":"text","text":"after"}]}]`, `[{"type":"text","text":"leading"},{"type":"tool_result","tool_use_id":"R","content":[`+part+`]},{"type":"tool_result","tool_use_id":"second","content":[`+image+`]},{"type":"text","text":"trailing"}]`)
	}
	for _, content := range contents {
		for _, options := range []string{`{}`, `{"roundTripReasoningContent":true,"passThinkingToggle":true,"passSamplingParams":true}`, `{"imageContentMode":"text_only"}`, `{"imageContentMode":"text_only","requestCompatibility":{"reasoning":"unsupported","sampling":"unsupported"}}`} {
			body := base.Clone()
			body.Set("messages", []byte(`[{"role":"user","content":`+content+`},{"role":"assistant","content":`+content+`}]`))
			body.Set("temperature", []byte(`0.2`))
			body.Set("top_p", []byte(`0.9`))
			rows = append(rows, chatSourceScenario{"request", requestEncode(body), requestObject([]byte(options))})
		}
	}
	for _, system := range []string{`"x-anthropic-billing-header: rotating\r\n\r\nreal\ud800"`, `"x-anthropic-billing-header: only"`, `"prefix\nx-anthropic-billing-header: retained"`, `[{"type":"text","text":"x-anthropic-billing-header: a\n\n"},{"type":"text","text":"second"},{"type":"text","text":""}]`} {
		body := base.Clone()
		body.Set("system", []byte(system))
		rows = append(rows, chatSourceScenario{"request", requestEncode(body), requestRecord()})
	}
	for _, choice := range []string{`"any"`, `"none"`, `"future"`, `null`, `{"type":"auto","disable_parallel_tool_use":true}`, `{"type":"any","disable_parallel_tool_use":false}`, `{"type":"tool","name":"Read"}`, `{"type":"tool","name":"missing"}`, `{"type":"tool","name":"BatchTool"}`} {
		for _, tools := range []string{`[]`, `[{"name":"BatchTool","input_schema":{}}]`, `[{"name":"Read","description":"read","input_schema":{"type":"object"}},{"name":"BatchTool","input_schema":{}}]`} {
			for _, options := range []string{`{}`, `{"requestCompatibility":{"parallelTools":"unsupported"}}`} {
				body := base.Clone()
				body.Set("tools", []byte(tools))
				body.Set("tool_choice", []byte(choice))
				rows = append(rows, chatSourceScenario{"request", requestEncode(body), requestObject([]byte(options))})
			}
		}
	}
	for _, thinking := range []string{`{"type":"enabled"}`, `{"type":"adaptive"}`, `{"type":"enabled","budget_tokens":null}`, `{"type":"enabled","budget_tokens":1024}`, `{"type":"enabled","budget_tokens":1025}`, `{"type":"enabled","budget_tokens":8192}`, `{"type":"enabled","budget_tokens":8193}`, `{"type":"enabled","budget_tokens":"bad"}`} {
		for _, effort := range []string{`null`, `"none"`, `"minimal"`, `"low"`, `"medium"`, `"high"`, `"xhigh"`, `"max"`, `"future"`} {
			body := base.Clone()
			body.Set("thinking", []byte(thinking))
			body.Set("output_config", []byte(`{"effort":`+effort+`}`))
			rows = append(rows, chatSourceScenario{"request", requestEncode(body), requestObject([]byte(`{"passThinkingToggle":true}`))})
		}
	}
	for _, model := range []string{"fixture", "o3", "gpt-6-sol"} {
		for _, options := range []string{`{}`, `{"budgetSource":"default"}`, `{"budgetSource":"default","requestCompatibility":{"maxOutputTokens":32,"outputTokenLimit":24}}`, `{"requestCompatibility":{"outputTokenField":"omit"}}`, `{"requestCompatibility":{"outputTokenField":"max_tokens"}}`, `{"requestCompatibility":{"outputTokenField":"max_completion_tokens"}}`} {
			body := base.Clone()
			body.Set("model", requestEncode(model))
			rows = append(rows, chatSourceScenario{"request", requestEncode(body), requestObject([]byte(options))})
		}
	}
	for _, format := range []string{`null`, `false`, `[]`, `"bad"`, `{}`, `{"type":"json_schema","schema":[]}`, `{"type":"json_schema","name":"","description":"","schema":{"type":"object"}}`, `{"type":"json_schema","name":"named","schema":{"type":"object"},"strict":true}`} {
		for _, options := range []string{`{}`, `{"requestCompatibility":{"structuredOutput":"unsupported"}}`} {
			body := base.Clone()
			body.Set("output_format", []byte(format))
			rows = append(rows, chatSourceScenario{"request", requestEncode(body), requestObject([]byte(options))})
		}
	}
	compareChatSource(t, rows, func(row chatSourceScenario) (json.RawMessage, error) {
		value, err := AnthropicToChat(requestObject(row.Value), row.Options)
		return requestEncode(value), err
	})
}
