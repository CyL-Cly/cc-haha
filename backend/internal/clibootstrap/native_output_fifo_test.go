package clibootstrap

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativePublicOutputFIFOEmitsExactlyOneSDKRecordDelimiter(t *testing.T) {
	var output bytes.Buffer
	receipt, err := protocol.EncodeNDJSON(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: []byte(`"system"`)}, protocol.JSONField{Name: "subtype", Value: []byte(`"session_message_receipt"`)}))
	if err != nil {
		t.Fatal(err)
	}
	m := &MainPrint{options: MainPrintOptions{Output: &output}, outputs: [][]byte{[]byte(`{"type":"control_response","response":{"subtype":"success"}}`), receipt, []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"embedded\\ntext"}]}}`)}, wake: make(chan struct{}, 1), finish: make(chan struct{}), outputDone: make(chan struct{})}
	close(m.finish)
	m.drain()
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatal("SDK FIFO inserted an empty record", output.String())
	}
	for _, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatal("public SDK record is not complete JSON", line)
		}
	}
	if m.outputError != nil {
		t.Fatal(m.outputError)
	}
}
