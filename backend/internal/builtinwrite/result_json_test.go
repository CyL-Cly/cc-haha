package builtinwrite

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestWriteResultConstructsStringsRatherThanCopyingInputJSONSpelling(t *testing.T) {
	s, o, _, _, _ := fixture(t)
	in := input("unused", "")
	in.Set("file_path", json.RawMessage(`"e\u0078act"`))
	in.Set("content", json.RawMessage(`"\u0061<\u0000\ud800"`))
	run(t, o, func(scope *cooperative.Scope) error {
		data, e := s.Call(context.Background(), in, scope)
		if e != nil {
			return e
		}
		var output protocol.JSONObject
		if e = json.Unmarshal(data, &output); e != nil {
			return e
		}
		if string(output.Get("filePath")) != `"exact"` {
			return fmt.Errorf("constructed filePath retained input spelling %s", output.Get("filePath"))
		}
		units, _ := protocol.DecodeJavaScriptJSONString(in.Get("content"))
		want := protocol.QuoteJavaScriptUTF16(units)
		if string(output.Get("content")) != string(want) {
			return fmt.Errorf("constructed content %s want%s", output.Get("content"), want)
		}
		return nil
	})
}
