package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualBashBackgroundNotificationReentersExistingHarness(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, f.config+"/settings.json", `{"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-background-fixture"
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		round := calls.Add(1)
		if round == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			input, _ := json.Marshal(`{"command":"printf started; sleep 0.3; printf finished","description":"actual notification","run_in_background":true}`)
			fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"background-model\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
			fmt.Fprintf(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"background-shell-use\",\"name\":\"Bash\",\"input\":{}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%s}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", input)
			return
		}
		if round == 2 && !bytes.Contains(body, []byte("Command running in background with ID:")) {
			t.Errorf("background tool result missing: %.1600s", body)
		}
		if round == 3 && (!bytes.Contains(body, []byte("<task-notification>")) || !bytes.Contains(body, []byte("actual notification")) || !bytes.Contains(body, []byte(`"isMeta"`))) {
			t.Logf("provider strips metadata; notification body %.1600s", body)
			if !bytes.Contains(body, []byte("<task-notification>")) {
				t.Error("actual notification never reentered Harness")
			}
		}
		if round > 3 {
			t.Error("duplicate notification model followup")
		}
		emitPlanFixture(w, int(round), "", "task turn done")
	}))
	defer model.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual native background notification Harness", Tools: []string{"Bash,TaskOutput,TaskStop"}, MaxTurns: 5, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e = m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Run actual shell in background"})})
	if e = m.RunTurn(ctx, input); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected tool round, human completion, actual background notification round; requests=%d", calls.Load())
	}
	history, _ := json.Marshal(m.Controller.History())
	if !bytes.Contains(history, []byte(`"isMeta":true`)) || !bytes.Contains(history, []byte("task-notification")) {
		t.Fatalf("notification is not actual canonical meta user: %.1800s", history)
	}
}
