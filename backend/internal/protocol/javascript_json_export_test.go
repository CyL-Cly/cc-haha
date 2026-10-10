package protocol

import (
	"testing"
)

func TestParseAndStringifyJavaScriptJSONPreservesRecoverySourceBehavior(t *testing.T) {
	for _, test := range []struct {
		input    string
		expected string
	}{
		{`{"text":"\u4e2d\u6587\ud83d\ude00","lone":"\ud800","slash":"\/","escaped":"\n\t"}`, `{"text":"中文😀","lone":"\ud800","slash":"/","escaped":"\n\t"}`},
		{`{"2":"two","z":1,"1":"first","2":"last","integer":9007199254740993,"overflow":1e400}`, `{"1":"first","2":"last","z":1,"integer":9007199254740992,"overflow":null}`},
		{` [ 1e-400, -0, null, true ] `, `[0,0,null,true]`},
		{`"\udfff"`, `"\udfff"`},
	} {
		output, err := ParseAndStringifyJavaScriptJSON([]byte(test.input))
		if err != nil || string(output) != test.expected {
			t.Fatalf("Recovery JSON output semantics changed: %q, %v; want %q", output, err, test.expected)
		}
	}
	for _, invalid := range []string{"", "{", "null true", "{\"x\":1,}", "undefined"} {
		output, err := ParseAndStringifyJavaScriptJSON([]byte(invalid))
		if err == nil || output != nil {
			t.Fatalf("invalid Recovery JSON acquired output: %q, %v", output, err)
		}
	}
}
