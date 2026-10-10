package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestActualPublicSDKInitializeWorktreeHooksBeforeAnyModelInput(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, f.config+"/settings.json", `{"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-sdk-worktree-fake-key"
	var output, diagnostics bytes.Buffer
	disabled := false
	input := `{"type":"control_request","request_id":"initialize629","request":{"subtype":"initialize","hooks":{"WorktreeCreate":[{"hookCallbackIds":["create629"]}],"WorktreeRemove":[{"hookCallbackIds":["remove629"]}]}}}` + "\n"
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(input), Output: &output, Error: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err = m.Run(ctx); err != nil {
		t.Fatal("actual public SDK worktree init", err, diagnostics.String())
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	matched := false
	for {
		var row map[string]json.RawMessage
		if err = decoder.Decode(&row); err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err, output.String())
		}
		var kind string
		_ = json.Unmarshal(row["type"], &kind)
		if kind == "control_response" {
			var response map[string]json.RawMessage
			_ = json.Unmarshal(row["response"], &response)
			if string(response["request_id"]) == `"initialize629"` && string(response["subtype"]) == `"success"` {
				matched = true
			}
		}
	}
	if !matched {
		t.Fatal("public SDK initialize did not return its actual response", output.String())
	}
}
