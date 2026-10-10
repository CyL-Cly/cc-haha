package provider

import (
	"strings"
	"testing"
)

func TestSDKAPIErrorPreservesValidDeepFutureAndShallowMessage(t *testing.T) {
	deep := strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001)
	raw := `{"message":"selected","future":` + deep + `}`
	got := sdkAPIError(nil, raw, nil, false)
	if got.Message != "selected" || string(got.Payload) != raw {
		t.Fatal(got.Message, len(got.Payload))
	}
	for _, test := range []struct{ raw, message string }{
		{`{"future":` + deep + `,"message":"old","message":"last"}`, "last"},
		{`{"m\u0065ssage":"escaped-key","future":` + deep + `}`, "escaped-key"},
		{`{"message":{"deep":` + deep + `}}`, `{"deep":` + deep + `}`},
		{`{"future":"escaped\\\"brace}:,\\tail","message":"selected"}`, "selected"},
		{`{"message":false,"future":true}`, `{"message":false,"future":true}`},
		{`{"other":1}`, `{"other":1}`},
		{`{}`, `{}`},
		{`{"message":"old","message":false}`, `{"message":false}`},
		{`[1,2]`, `[1,2]`},
	} {
		got := sdkAPIError(nil, test.raw, nil, false)
		if got.Message != test.message || string(got.Payload) != test.raw {
			t.Fatal(got.Message[:min(len(got.Message), 80)], test.message[:min(len(test.message), 80)])
		}
	}
}
