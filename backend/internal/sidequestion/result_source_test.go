package sidequestion

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/apipresentation"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func TestActualSourceSideResponseTextAPIErrorAndRawUTF16(t *testing.T) {
	assistant := func(content string, api bool) json.RawMessage {
		return json.RawMessage(`{"type":"assistant","isApiErrorMessage":` + string(quote(api)) + `,"message":{"content":` + content + `}}`)
	}
	system := func(errorJSON string) json.RawMessage {
		return json.RawMessage(`{"type":"system","subtype":"api_error","error":` + errorJSON + `}`)
	}
	histories := [][]json.RawMessage{
		{}, {assistant(`[]`, false)}, {assistant(`[{"type":"thinking","thinking":"secret"}]`, false)},
		{assistant(`[{"type":"text","text":"  text \ud800 \ufeff "}]`, false)},
		{assistant(`[{"type":"thinking","thinking":"secret"}]`, false), assistant(`[{"type":"text","text":"first"}]`, false), assistant(`[{"type":"text","text":"second\udfff"}]`, false)},
		{assistant(`[{"type":"text","text":"API Error: \ud800"},{"type":"text","text":" second "}]`, true)},
		{assistant(`[]`, true)},
		{assistant(`[{"type":"text","text":"already"}]`, false), assistant(`[{"type":"text","text":"real failure"}]`, true)},
		{assistant(`[{"type":"tool_use","id":"one","name":"Write","input":{}}]`, false)},
		{assistant(`[{"type":"text","text":"prior"},{"type":"tool_use","id":"one","name":"\ud800","input":{}}]`, false)},
		{system(`{"actualError":true,"message":"transient"}`), assistant(`[{"type":"text","text":"recovered"}]`, false)},
	}
	for _, errorJSON := range []string{
		`{"actualError":true,"message":"upstream \udfff"}`,
		`{"actualError":true,"message":"Connection error."}`,
		`{"actualError":true,"message":"Connection error.","cause":{"actualError":true,"message":"cause","code":"ECONNRESET"}}`,
		`{"actualError":true,"message":"arbitrary","code":"ETIMEDOUT"}`,
		`{"actualError":true,"message":"outer","cause":{"actualError":true,"message":"tls","code":"CERT_HAS_EXPIRED"}}`,
		`{"actualError":true,"message":"outer","cause":{"actualError":true,"message":"tls","code":"CERT_SIGNATURE_FAILURE"}}`,
		`{"actualError":true,"message":"<!DOCTYPE html><title>  upstream \ud800\ufeff </title><body>hidden</body>"}`,
		`{"actualError":true,"message":"<html>hidden no title"}`,
		`{"status":403,"error":{"error":{"message":"nested \ud800"}}}`,
		`{"status":502,"error":{"message":"<html><title> upstream \udfff </title>"}}`,
		`{"status":null}`, `{}`, `{"status":400,"error":{"error":{"message":"<html>no title"},"message":"fallback"}}`,
	} {
		histories = append(histories, []json.RawMessage{system(errorJSON)})
	}
	actual := []protocol.JSONObject{}
	for _, history := range histories {
		records := []protocol.JSONObject{}
		var fault json.RawMessage
		var failure error
		for _, raw := range history {
			row := object(raw)
			records = append(records, row)
			if e := CheckMessage(row); e != nil {
				failure = e
				break
			}
			if row.String("type") == "system" {
				spec := object(row.Get("error"))
				if string(spec.Get("actualError")) == "true" {
					value, e := apipresentation.FormatAPIErrorText(sideResultFixtureError(spec))
					if e != nil {
						t.Fatal(e)
					}
					fault = value
				}
			}
		}
		var value json.RawMessage
		if failure == nil {
			value, failure = ExtractWithAPIError(records, fault)
		}
		result := protocol.NewJSONObject()
		if failure != nil {
			result.Set("error", ErrorMessageJSON(failure))
		} else {
			result.Set("response", value)
		}
		actual = append(actual, result)
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../testdata/side-question-result.fixture.ts")
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SIDE_QUESTION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=test", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	input, _ := json.Marshal(map[string]any{"histories": histories})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	source, e := cmd.Output()
	if e != nil {
		t.Fatal("actual Source", e, stderr.String())
	}
	// Compare raw JS serialization, not Go strings which collapse lone UTF16.
	data, _ := json.Marshal(actual)
	expected, e := protocol.ParseAndStringifyJavaScriptJSON(source)
	if e != nil {
		t.Fatal(e, string(source))
	}
	got, e := protocol.ParseAndStringifyJavaScriptJSON(data)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(expected, got) {
		t.Fatalf("actual Source%s Go%s", expected, got)
	}
}
func sideResultFixtureError(spec protocol.JSONObject) error {
	status := (*int)(nil)
	var n int
	if raw := spec.Get("status"); raw != nil && string(raw) != "null" && json.Unmarshal(raw, &n) == nil {
		status = &n
	}
	var cause error
	if raw := spec.Get("cause"); raw != nil {
		row := object(raw)
		if string(row.Get("actualError")) == "true" {
			cause = sideResultFixtureError(row)
		}
	}
	sdk := &provider.SDKStreamError{MessageJSON: spec.Get("message"), Status: status, Payload: spec.Get("error"), Cause: cause}
	if code := spec.Get("code"); code != nil {
		return &sideResultCodedError{sdk, code}
	}
	return sdk
}

type sideResultCodedError struct {
	*provider.SDKStreamError
	code json.RawMessage
}

func (e *sideResultCodedError) ConnectionCodeJSON() json.RawMessage { return e.code }
