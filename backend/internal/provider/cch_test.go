package provider

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCCHHashReferenceAndOrder(t *testing.T) {
	for input, expected := range map[string]uint64{"": 0xef46db3751d8e999, "a": 0xd24ec4f1a98c6e5b, "hello world": 0x45ab6734b21e6968} {
		if actual := xxHash64Seeded([]byte(input), 0); actual != expected {
			t.Fatalf("xxhash %q=%x, expected %x", input, actual, expected)
		}
	}
	first := `{"model":"fixture-model","messages":[{"role":"user","content":"hi"}],"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.281.abc; cc_entrypoint=sdk; cch=00000;"}],"max_tokens":32,"stream":true}`
	second := strings.Replace(strings.Replace(first, `"model":"fixture-model","messages"`, `"messages"`, 1), `,"max_tokens":32`, `,"model":"fixture-model","max_tokens":32`, 1)
	if got := SignClaudeCodeCCHInString(first); got != strings.Replace(first, "cch=00000;", "cch=fe4f0;", 1) {
		t.Fatalf("real TS signature differs: %s", got)
	}
	if got := SignClaudeCodeCCHInString(second); !strings.Contains(got, "cch=5df72;") {
		t.Fatalf("property order signature differs: %s", got)
	}
	if SignClaudeCodeCCHInString(first+"\n") == SignClaudeCodeCCHInString(first)+"\n" {
		t.Fatal("trailing newline must participate in the signature")
	}
}

func TestCCHStructuredBillingAndByteDecoding(t *testing.T) {
	billing := `"x-anthropic-billing-header: cch=00000;"`
	for _, body := range []string{"", "invalid x-anthropic-billing-header: cch=00000;", `{"system":[` + billing + `]}`, `{"system":null}`, `{"system":{"text":` + billing + `}}`, `{"system":[{"type":"image","text":` + billing + `}]}`, `{"system":` + billing + `,"duplicate":` + billing + `}`, `{"system":"x-anthropic-billing-header: xcch=00000;"}`, `{"system":"\u0078-anthropic-billing-header: cch=00000;"}`, `{"system":true,"other":` + billing + `}`, `[]`} {
		if SignClaudeCodeCCHInString(body) != body {
			t.Fatalf("bypass body was changed: %s", body)
		}
	}
	for _, body := range []string{`{"system":` + billing + `}`, `{"system":[{"type":"\u0074ext","text":` + billing + `}]}`, `{"system":[{"type":"text","text":` + billing + `},{"type":"text","text":"other"}]}`} {
		if SignClaudeCodeCCHInString(body) == body {
			t.Fatalf("recognized billing was not signed: %s", body)
		}
	}
	invalid := []byte{0xff, 0xfe, 0xe2, 0x82}
	if got := SignClaudeCodeCCHBody(invalid); !bytes.Equal(got, invalid) || &got[0] != &invalid[0] {
		t.Fatal("unsigned invalid bytes must remain the original slice")
	}
	if got := decodeTextDecoder(invalid); got != "\ufffd\ufffd\ufffd" {
		t.Fatalf("TextDecoder invalid/truncated sequences=%q", got)
	}
	for _, fixture := range []struct {
		bytes []byte
		text  string
	}{
		{[]byte{0xc2, 'x'}, "\ufffdx"},
		{[]byte{0xe2, 0x82, 'x'}, "\ufffdx"},
		{[]byte{0xe0, 0x80, 'x'}, "\ufffd\ufffdx"},
		{[]byte{0xed, 0xa0, 0x80}, "\ufffd\ufffd\ufffd"},
		{[]byte{0xf0, 0x80, 'x'}, "\ufffd\ufffdx"},
		{[]byte{0xf4, 0x90, 'x'}, "\ufffd\ufffdx"},
		{[]byte{0xf1, 0x80, 0x80, 'x'}, "\ufffdx"},
	} {
		if got := decodeTextDecoder(fixture.bytes); got != fixture.text {
			t.Fatalf("TextDecoder %x=%q expected %q", fixture.bytes, got, fixture.text)
		}
	}
	body := []byte(`{"system":` + billing + `,"text":"` + string([]byte{0xff}) + `"}`)
	signed := SignClaudeCodeCCHBody(append([]byte{0xef, 0xbb, 0xbf}, body...))
	if !json.Valid(signed) || bytes.HasPrefix(signed, []byte{0xef, 0xbb, 0xbf}) || bytes.Contains(signed, []byte(cchPlaceholder)) {
		t.Fatalf("signed byte path must encode decoded text without BOM: %s", signed)
	}
}
