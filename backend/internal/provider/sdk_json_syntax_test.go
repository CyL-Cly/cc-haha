package provider

import (
	"strings"
	"testing"
)

// Messages were captured from actual JSON.parse in pinned Bun 1.3.14. They
// intentionally differ from encoding/json's diagnostics and offset wording.
func TestSDKJSONParsePinnedRuntimeDiagnostics(t *testing.T) {
	for _, test := range []struct{ source, message string }{
		{"", "Unexpected EOF"}, {"[DONE]", `Unexpected identifier "DONE"`}, {"not-json", `Unexpected identifier "not"`},
		{"{bad:1}", "Expected '}'"}, {`{"a":1,}`, "Property name must be a string literal"}, {"[1,]", "Unexpected comma at the end of array expression"},
		{`"tail`, "Unterminated string"}, {"\"a\nb\"", "Unterminated string"}, {`"\x"`, "Invalid escape character x"},
		{`"\uZZZZ"`, `"\uZZZZ" is not a valid unicode escape`}, {`"\u12"`, `\u must be followed by 4 hex digits`},
		{"01", "Unable to parse JSON string"}, {`{"a":1}tail`, "Unable to parse JSON string"}, {`{"a":`, "Unexpected EOF"},
		{"{", "Expected '}'"}, {"[", "Unexpected EOF"}, {"}", "Unexpected token '}'"}, {"]", "Unexpected token ']'"}, {":", "Unexpected token ':'"},
		{",", "Unexpected token ','"}, {"@", "Unrecognized token '@'"}, {"nullx", "Unable to parse JSON string"},
		{"-", "Invalid number"}, {"-.1", "Invalid number"}, {".1", "Unexpected token '.'"}, {"1.", "Invalid digits after decimal point"},
		{"1e", "Unable to parse JSON string"}, {"1e+", "Unable to parse JSON string"}, {"--1", "Invalid number"},
		{"[1 2]", "Expected ']'"}, {`{"a" 1}`, "Expected ':' before value in object property definition"}, {`{"a":1 "b":2}`, "Expected '}'"},
		{`{"a":1,,}`, "Property name must be a string literal"}, {"[,,]", "Unexpected token ','"}, {"[1,,2]", "Unexpected token ','"},
		{"true false", "Unable to parse JSON string"}, {"/*hi*/ {}", "Unrecognized token '/'"}, {"'abc'", "Single quotes (') are not allowed in JSON"},
		{"[1e]", "Expected ']'"}, {`{"a":1e}`, "Expected '}'"}, {`{"a":}`, "Unexpected token '}'"},
		{`{"a",1}`, "Expected ':' before value in object property definition"}, {`{"a"`, "Expected ':' before value in object property definition"},
		{`"abc\`, "Unterminated string"}, {"[nullx]", "Expected ']'"}, {`{"a":01}`, "Expected '}'"}, {"[1.,2]", "Invalid digits after decimal point"},
		{"{ @}", "Unrecognized token '@'"}, {"+1", "Unrecognized token '+'"}, {"(1)", "Unexpected token '('"}, {"é", "Unrecognized token 'é'"},
	} {
		t.Run(test.source, func(t *testing.T) {
			want := "JSON Parse error: " + test.message
			if got := sdkJSONParseMessage(test.source); got != want {
				t.Fatalf("got %q want %q", got, want)
			}
		})
	}
}

func TestSDKJSONDiagnosticParserNeverNormalizesValidNumbersOrUTF16(t *testing.T) {
	for _, source := range []string{`null`, `false`, `true`, `0`, `-0`, `1e400`, `-1.1E+200`, `"\ud800"`, `[1,"a",null]`, `{"a":{"b":[]},"a":2}`, strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001)} {
		if got := sdkJSONSyntaxError(source); got != nil {
			t.Fatal(source[:min(len(source), 60)], got)
		}
	}
	if got := sdkJSONSyntaxError("🙂"); got == nil || string(got.MessageJSON) != `"JSON Parse error: Unrecognized token '\ud83d'"` {
		t.Fatal(got)
	}
}

func TestPinnedJavaScriptJSONSyntaxMessageForConfigurationReaders(t *testing.T) {
	for _, source := range []string{`null`, `1e400`, `"\ud800"`, `{"key":1,"key":2}`} {
		if message, invalid := PinnedJavaScriptJSONSyntaxMessage(source); invalid || message != nil {
			t.Fatalf("valid JavaScript JSON %q was rejected: %s", source, message)
		}
	}
	for _, test := range []struct{ source, want string }{
		{`{"a":1,}`, `"JSON Parse error: Property name must be a string literal"`},
		{"🙂", `"JSON Parse error: Unrecognized token '\ud83d'"`},
		{"", `"JSON Parse error: Unexpected EOF"`},
	} {
		message, invalid := PinnedJavaScriptJSONSyntaxMessage(test.source)
		if !invalid || string(message) != test.want {
			t.Fatalf("source %q: %s, invalid=%t; want %s", test.source, message, invalid, test.want)
		}
	}
}
