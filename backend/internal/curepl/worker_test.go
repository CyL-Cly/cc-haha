package curepl

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func workerFixture(t *testing.T) (*worker, *[]workerMessage) {
	t.Helper()
	messages := []workerMessage{}
	w, err := newWorker(func(m workerMessage) error { messages = append(messages, m); return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.owner.Close)
	if err = w.receive(context.Background(), workerMessage{Type: "init", Bootstrap: BootstrapSource()}); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Type != "ready" {
		t.Fatal(messages)
	}
	messages = nil
	return w, &messages
}
func cellFixture(t *testing.T, w *worker, messages *[]workerMessage, id int64, code string) []workerMessage {
	t.Helper()
	*messages = nil
	raw, _ := json.Marshal(code)
	if err := w.receive(context.Background(), workerMessage{Type: "run", CellID: id, Code: raw}); err != nil {
		t.Fatal(err)
	}
	return *messages
}
func textEmitted(t *testing.T, messages []workerMessage) []string {
	t.Helper()
	var text []string
	for _, message := range messages {
		if message.Type == "emit" {
			var row struct{ Text string }
			if err := json.Unmarshal(message.Content, &row); err != nil {
				t.Fatal(err)
			}
			text = append(text, row.Text)
		}
	}
	return text
}
func TestGuestCellsRetainClosuresAndReachedBindingsAfterFailure(t *testing.T) {
	w, messages := workerFixture(t)
	cellFixture(t, w, messages, 1, `let count = 2; const readCount = () => count; nodeRepl.write(readCount())`)
	rows := cellFixture(t, w, messages, 2, `count = 3; nodeRepl.write(readCount()); let reached = 42; throw new Error('ordinary failure')`)
	if got := textEmitted(t, rows); len(got) != 1 || got[0] != "3" || rows[len(rows)-1].Error != "ordinary failure" {
		t.Fatal(rows, got)
	}
	rows = cellFixture(t, w, messages, 3, `nodeRepl.write([readCount(),reached]);`)
	if got := textEmitted(t, rows); len(got) != 1 || got[0] != "[3,42]" || rows[len(rows)-1].Error != "" {
		t.Fatal(rows, got)
	}
}
func TestGuestHasNoNodeFilesystemNetworkOrDynamicCodeGeneration(t *testing.T) {
	w, messages := workerFixture(t)
	rows := cellFixture(t, w, messages, 1, `nodeRepl.write([typeof process,typeof require,typeof fetch,typeof Bun,typeof Buffer]); let failures=[]; for(const f of [()=>eval('1'),()=>Function('return 1')(),()=>({}).constructor.constructor('return 1')(),()=>Object.getPrototypeOf(async()=>{}).constructor('return 1')()]) { try { f() } catch(e) { failures.push(e.name) } }; nodeRepl.write(failures);`)
	got := textEmitted(t, rows)
	if len(got) != 2 || got[0] != `["undefined","undefined","undefined","undefined","undefined"]` || got[1] != `["EvalError","EvalError","EvalError","EvalError"]` {
		t.Fatal(rows, got)
	}
}
func TestGuestBridgeUsesCurrentObservedAppAndSerialJSONOnly(t *testing.T) {
	w, messages := workerFixture(t)
	rows := cellFixture(t, w, messages, 1, `let app = await cua.getApp('Fixture App'); nodeRepl.write(app);`)
	if len(rows) != 1 || rows[0].Type != "invoke" || rows[0].Name != "get_app_state" {
		t.Fatal(rows)
	}
	result := json.RawMessage(`{"content":[{"type":"text","text":"<app_state>\ng1:3 button Run\n</app_state>"}],"app":"/Applications/Fixture.app"}`)
	if err := w.receive(context.Background(), workerMessage{Type: "response", CellID: 1, RequestID: rows[0].RequestID, Result: result}); err != nil {
		t.Fatal(err)
	}
	rows = cellFixture(t, w, messages, 2, `await app.click(3);`)
	if len(rows) != 1 || rows[0].Type != "invoke" || rows[0].Name != "click" || !strings.Contains(string(rows[0].Args), `"app":"/Applications/Fixture.app"`) || !strings.Contains(string(rows[0].Args), `"element_index":"g1:3"`) {
		t.Fatal(rows)
	}
}
func TestGuestTimersAwaitAndClearAfterCellSettlement(t *testing.T) {
	w, messages := workerFixture(t)
	cellFixture(t, w, messages, 1, `await new Promise(resolve => setTimeout(resolve, 1)); nodeRepl.write('timer settled'); setTimeout(()=>nodeRepl.write('late output'),1);`)
	time.Sleep(3 * time.Millisecond)
	if err := w.fireTimers(); err != nil {
		t.Fatal(err)
	}
	if w.active != nil {
		t.Fatal("cell did not finish")
	}
	got := textEmitted(t, *messages)
	if len(got) != 1 || got[0] != "timer settled" {
		t.Fatal(*messages, got)
	}
}
