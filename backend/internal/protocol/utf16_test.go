package protocol

import (
	"reflect"
	"testing"
)

func TestPublicUTF16StringBoundaryPreservesSplitSurrogates(t *testing.T) {
	units, err := DecodeJavaScriptJSONString([]byte(` "中文 😀\ud800\n" `))
	want := []uint16{'中', '文', ' ', 0xd83d, 0xde00, 0xd800, '\n'}
	if err != nil || !reflect.DeepEqual(units, want) {
		t.Fatal(units, err)
	}
	if got := string(QuoteJavaScriptUTF16(units[:4])); got != `"中文 \ud83d"` {
		t.Fatal(got)
	}
	if got := string(QuoteJavaScriptUTF16(units)); got != `"中文 😀\ud800\n"` {
		t.Fatal(got)
	}
	if got := string(QuoteJavaScriptUTF16(nil)); got != `""` {
		t.Fatal(got)
	}
	for _, raw := range []string{"", `null`, `[]`, `{"value":"x"}`, `"broken`, `"one" "two"`} {
		if _, err := DecodeJavaScriptJSONString([]byte(raw)); err == nil {
			t.Fatal("invalid string accepted", raw)
		}
	}
}
