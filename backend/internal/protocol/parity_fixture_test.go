package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProtocolParityFixture is a test-only cross-language consumer. The Bun
// parity lane compiles this package's test binary and supplies a disposable
// fixture; there is no extra runtime command or provider/config access.
func TestProtocolParityFixture(t *testing.T) {
	path := os.Getenv("CC_HAHA_PROTOCOL_FIXTURE")
	if path == "" {
		t.Skip("only used by the hermetic TypeScript/Go differential lane")
	}
	home := os.Getenv("HOME")
	if home == "" || filepath.Dir(path) != home || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") {
		t.Fatal("Protocol fixture requires explicit disposable HOME/config")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Wire      *string `json:"wire"`
		WireBytes []byte  `json:"wireBytes"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if (fixture.Wire != nil) == (fixture.WireBytes != nil) {
		t.Fatal("Protocol fixture requires exactly one valid wire or wireBytes input")
	}
	wire := fixture.WireBytes
	if fixture.Wire != nil {
		wire = []byte(*fixture.Wire)
	}
	message, decodeErr := DecodeAnthropicStream(context.Background(), strings.NewReader(string(wire)), nil)
	var usage any
	content := []ContentBlock{}
	var id, stop any = "", ""
	if message != nil {
		content = message.Blocks()
		if raw := (*message)["id"]; len(raw) > 0 {
			id = raw
		}
		if raw := (*message)["stop_reason"]; len(raw) > 0 && string(raw) != "null" {
			stop = raw
		}
		if len(content) > 0 {
			usage = map[string]int64{
				"input_tokens": message.Usage().Int64("input_tokens"), "output_tokens": message.Usage().Int64("output_tokens"),
				"cache_creation_input_tokens": message.Usage().Int64("cache_creation_input_tokens"), "cache_read_input_tokens": message.Usage().Int64("cache_read_input_tokens"),
			}
		}
	}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	contentJSON, err = ParseAndStringifyJavaScriptJSON(contentJSON)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]any{"messageID": id, "stopReason": stop, "content": content, "contentBytes": string(contentJSON), "failed": decodeErr != nil, "usage": usage}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("GO_PROTOCOL_RESULT:%s\n", encoded)
}
