package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/requestmapping"
)

func parseBodyParams(t *testing.T, source string) *requestmapping.Value {
	t.Helper()
	value, err := requestmapping.ParseValue(json.RawMessage(source))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestBetaMessageOrderDeprecatedFormatAndInputIsolation(t *testing.T) {
	params := parseBodyParams(t, `{"z":1,"output_config":{"effort":"low","format":null,"a":2},"output_format":{"schema":{"z":1,"a":2}},"betas":["zeta","alpha"],"2":2,"1":1,"future":{"\ud800":"high","\udfff":"low"}}`)
	before, _ := params.MarshalJSON()
	result, err := PrepareBetaMessages(params, BetaMessageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"1":1,"2":2,"z":1,"output_config":{"effort":"low","format":{"schema":{"z":1,"a":2}},"a":2},"future":{"\ud800":"high","\udfff":"low"}}`
	if string(result.Body) != expected || result.Path != "/v1/messages?beta=true" || result.Headers.Get("anthropic-beta") != "zeta,alpha" {
		t.Fatalf("ordered SDK projection=%+v", result)
	}
	after, _ := params.MarshalJSON()
	if string(before) != string(after) {
		t.Fatal("SDK projection mutated params")
	}
	_, err = PrepareBetaMessages(parseBodyParams(t, `{"output_format":{},"output_config":{"format":true}}`), BetaMessageOptions{})
	if err == nil || err.Error() != outputFormatConflict {
		t.Fatalf("conflicting formats: %v", err)
	}
	for _, value := range []*requestmapping.Value{nil, parseBodyParams(t, "[]"), parseBodyParams(t, "false")} {
		if _, failure := PrepareBetaMessages(value, BetaMessageOptions{}); failure != ErrUnsupportedMessageParams {
			t.Fatalf("unresolved SDK arguments must remain explicitly unported: %v", failure)
		}
	}
}

func TestBetaMessageNumbersTruthinessAndUTF16Spread(t *testing.T) {
	for _, fixture := range []struct{ source, expected string }{
		{`{"output_format":1e400}`, `{"output_config":{"format":null}}`},
		{`{"output_format":1e-400}`, `{"output_format":0}`},
		{`{"output_format":false}`, `{"output_format":false}`},
		{`{"output_format":[],"output_config":"😀"}`, `{"output_config":{"0":"\ud83d","1":"\ude00","format":[]}}`},
		{`{"output_format":{},"output_config":["x","y"]}`, `{"output_config":{"0":"x","1":"y","format":{}}}`},
		{`{"output_format":{},"output_config":1}`, `{"output_config":{"format":{}}}`},
		{`{"output_format":{},"future":9007199254740993}`, `{"future":9007199254740992,"output_config":{"format":{}}}`},
	} {
		result, err := PrepareBetaMessages(parseBodyParams(t, fixture.source), BetaMessageOptions{})
		if err != nil || string(result.Body) != fixture.expected {
			t.Fatalf("%s => %s (%v), expected %s", fixture.source, result.Body, err, fixture.expected)
		}
	}
	result, err := PrepareBetaMessages(parseBodyParams(t, `{"betas":[1e400,[1e400,null],false],"output_config":null}`), BetaMessageOptions{})
	if err != nil || result.Headers.Get("anthropic-beta") != "Infinity,Infinity,,false" {
		t.Fatalf("beta String coercion lost nonfinite values: %+v %v", result, err)
	}
}

func TestBetaHeadersOverridesTombstonesAndRepeatedCase(t *testing.T) {
	text := func(value string) *string { return &value }
	params := parseBodyParams(t, `{"betas":["source"]}`)
	result, err := PrepareBetaMessages(params, BetaMessageOptions{HelperNames: []string{"mcpTool", "mcpMessage", "mcpTool"}, Headers: []HeaderEntry{{Name: "Anthropic-Beta", Values: []*string{text(" alpha "), text("beta")}}, {Name: "anthropic-beta", Values: []*string{text("gamma")}}, {Name: "x-stainless-helper", Values: []*string{nil}}, {Name: "X-Api-Key", Values: []*string{nil}}, {Name: "unused"}, {Name: "Same", Values: []*string{text("old")}}, {Name: "Same", Values: []*string{text("new")}}}})
	if err != nil || result.Headers.Get("anthropic-beta") != "gamma" || result.Headers.Get("same") != "new" || strings.Join(result.RemovedHeaders, ",") != "x-api-key,x-stainless-helper" {
		t.Fatalf("object-header override=%+v %v", result, err)
	}
	result, _ = PrepareBetaMessages(params, BetaMessageOptions{HeaderKind: "tuples", HelperNames: []string{"mcpTool", "mcpTool"}, Headers: []HeaderEntry{{Name: "Anthropic-Beta", Values: []*string{text("alpha")}}, {Name: "anthropic-beta", Values: []*string{text("beta")}}, {Name: "x-api-key", Values: []*string{nil, text("restored")}}}})
	if result.Headers.Get("anthropic-beta") != "alpha, beta" || result.Headers.Get("x-stainless-helper") != "mcpTool" || result.Headers.Get("x-api-key") != "restored" || len(result.RemovedHeaders) != 0 {
		t.Fatalf("tuple/header restoration=%+v", result)
	}
	_, err = PrepareBetaMessages(params, BetaMessageOptions{Headers: []HeaderEntry{{Name: "content-type", Values: []*string{text("application/x-www-form-urlencoded")}}}})
	if err != ErrUnsupportedMessageEncoding {
		t.Fatalf("pending SDK encoder must not silently send JSON: %v", err)
	}
}

// A real Go component fixture consumer in the package test executable avoids
// adding an otherwise untested command. All production behavior delegates to
// PrepareBetaMessages/the signer; this adapter only projects observations.
func TestMessageBodyFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_MESSAGE_BODY_FIXTURE") != "1" {
		t.Skip("offline parity fixture entry only")
	}
	var input struct {
		HomeDir string
		Steps   []struct {
			Operation    string
			Body         string
			Bytes        *[]int
			ParamsSource string
			Headers      *struct {
				Kind           string
				Entries        [][2]json.RawMessage
				UndefinedNames []string
			}
			Helpers []struct{ Name string }
		}
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil || json.Unmarshal(raw, &input) != nil || input.HomeDir == "" || input.HomeDir != os.Getenv("HOME") || filepath.Join(input.HomeDir, ".claude") != os.Getenv("CLAUDE_CONFIG_DIR") {
		t.Fatal("Message body fixture requires explicit disposable HOME/config")
	}
	results := []any{}
	for _, step := range input.Steps {
		if step.Operation == "sign" {
			if step.Bytes != nil {
				body := make([]byte, len(*step.Bytes))
				for index, value := range *step.Bytes {
					body[index] = byte(value)
				}
				signed := SignClaudeCodeCCHBody(body)
				identity := len(body) == 0 || &body[0] == &signed[0]
				values := make([]int, len(signed))
				for index, value := range signed {
					values[index] = int(value)
				}
				results = append(results, map[string]any{"operation": "sign", "bytes": values, "identitySame": identity})
			} else {
				results = append(results, map[string]any{"operation": "sign", "body": SignClaudeCodeCCHInString(step.Body)})
			}
			continue
		}
		params := parseBodyParams(t, step.ParamsSource)
		before, _ := params.MarshalJSON()
		options := BetaMessageOptions{}
		for _, marker := range step.Helpers {
			// Fixture markers are declared in the SDK's tools/message/block scan
			// order. Helper discovery upstream is explicitly outside this seam.
			options.HelperNames = append(options.HelperNames, marker.Name)
		}
		if step.Headers != nil {
			options.HeaderKind = step.Headers.Kind
			for _, pair := range step.Headers.Entries {
				var name string
				_ = json.Unmarshal(pair[0], &name)
				entry := HeaderEntry{Name: name}
				if pair[1][0] == '[' {
					_ = json.Unmarshal(pair[1], &entry.Values)
				} else {
					var value *string
					_ = json.Unmarshal(pair[1], &value)
					entry.Values = []*string{value}
				}
				options.Headers = append(options.Headers, entry)
			}
			for _, name := range step.Headers.UndefinedNames {
				options.Headers = append(options.Headers, HeaderEntry{Name: name})
			}
		}
		prepared, failure := PrepareBetaMessages(params, options)
		var projection, signedBody, errorProjection any
		if failure != nil {
			errorProjection = map[string]string{"name": "Error", "message": failure.Error()}
		} else {
			body, _ := prepareBetaMessageBody(params)
			names := []string{}
			for name := range prepared.Headers {
				names = append(names, strings.ToLower(name))
			}
			sort.Strings(names)
			headers := [][2]string{}
			for _, name := range names {
				headers = append(headers, [2]string{name, prepared.Headers.Get(name)})
			}
			removed := prepared.RemovedHeaders
			if removed == nil {
				removed = []string{}
			}
			projection = map[string]any{"path": prepared.Path, "body": string(body), "headers": headers, "removedHeaders": removed}
			signedBody = string(prepared.Body)
		}
		after, _ := params.MarshalJSON()
		results = append(results, map[string]any{"operation": "prepare", "before": string(before), "after": string(after), "prepared": projection, "signedBody": signedBody, "error": errorProjection})
	}
	output, _ := json.Marshal(map[string]any{"results": results})
	fmt.Printf("MESSAGE_BODY_RESULT:%s\n", output)
}
