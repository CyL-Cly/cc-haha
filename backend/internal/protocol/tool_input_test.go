package protocol

import (
	"encoding/json"
	"testing"
)

func TestUnparsedInputKeepsUTF16BoundaryAndOriginalLength(t *testing.T) {
	units := make([]uint16, 2050)
	for index := range units {
		units[index] = 'x'
	}
	units[2047], units[2048] = 0xd83d, 0xde00
	raw := CreateUnparsedToolInput(units)
	var parsed struct {
		Marker struct {
			Raw json.RawMessage `json:"raw"`
			Len int             `json:"len"`
		} `json:"__unparsedToolInput"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	actual, err := DecodeJavaScriptJSONString(parsed.Marker.Raw)
	if err != nil || len(actual) != 2048 || actual[2047] != 0xd83d || parsed.Marker.Len != 2050 {
		t.Fatal(len(actual), parsed.Marker.Len, err)
	}
	if string(CreateUnparsedToolInput(nil)) != `{"__unparsedToolInput":{"raw":"","len":0}}` {
		t.Fatal("empty input marker changed")
	}
}
