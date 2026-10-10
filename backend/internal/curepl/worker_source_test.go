package curepl

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestWorkerActualOriginalLanguageAndAppFacade(t *testing.T) {
	type scenario struct {
		Cells         []string                   `json:"cells"`
		NativeResult  json.RawMessage            `json:"nativeResult,omitempty"`
		NativeResults map[string]json.RawMessage `json:"nativeResults,omitempty"`
	}
	rows := []scenario{
		{Cells: []string{`nodeRepl.write([typeof process,typeof require,typeof fetch,typeof Bun,typeof Buffer]);`}},
		{Cells: []string{`let value=1; const read=()=>value;`, `value++; nodeRepl.write(read());`, `const value=42; nodeRepl.write(value);`, `nodeRepl.write(read());`}},
		{Cells: []string{`let before=2; var old=3;`, `var old=5; let reached=4; throw new Error('ordinary failure');var unreachable=6;`, `nodeRepl.write([before,old,reached,typeof unreachable]);`}},
		{Cells: []string{`const value=1`, `value=2;nodeRepl.write(value)`, `nodeRepl.write(value)`}},
		{Cells: []string{`let {value=42,...rest}={};let [first,...tail]=[1,2,3]; nodeRepl.write({value,rest,first,tail})`}},
		{Cells: []string{`nodeRepl.write('');nodeRepl.write('\ud800');nodeRepl.write('中文🦊')`}},
		{Cells: []string{`nodeRepl.write(JSON.parse('"\\ud800"').charCodeAt(0));nodeRepl.write(JSON.stringify('\ud800'));`}},
		{Cells: []string{`await new Promise(resolve=>setTimeout(resolve,1));nodeRepl.write('timer');setTimeout(()=>nodeRepl.write('late'),1);`, `nodeRepl.write('next')`}},
		{Cells: []string{`try {Function('return process')()} catch(error){nodeRepl.write(error.name)}`, `try {eval('1')} catch(error){nodeRepl.write(error.name)}`}},
		{Cells: []string{`import x from 'node:fs'`, `return 3`}},
		{Cells: []string{`await cua.computer.list_apps();nodeRepl.write('dispatched')`}},
		{Cells: []string{`const app=await cua.getApp('Fixture');`, `await app.click(3);await app.paste('hello');nodeRepl.write('acted')`}, NativeResult: json.RawMessage(`{"content":[{"type":"text","text":"<app_state>\ng1:3 button Run\n</app_state>"}],"app":"/Applications/Fixture.app"}`)},
		// An opaque handle is not an ordinal and remains unchanged even when
		// quoted transcript text contains another plausible handle. The native
		// stale-element rejection is an ordinary cell error; app bindings survive
		// and the next cell can observe and use the documented coordinate path.
		{Cells: []string{
			`const app=await cua.getApp('Fixture');`,
			`await app.click('g2:585');nodeRepl.write('unreachable')`,
			`nodeRepl.write(await app.getAXState({disableDiffing:true,emit:false}));await app.click([600,666]);nodeRepl.write('recovered')`,
		}, NativeResult: json.RawMessage(`{"content":[]}`), NativeResults: map[string]json.RawMessage{
			"get_app_state": json.RawMessage(`{"content":[{"type":"text","text":"<app_state>\n\tg2:619 text quoted text entry area g2:585\n\tg2:585 text entry area (settable, string) Private composer\n</app_state>"}],"app":"/Applications/Fixture.app"}`),
			"click":         json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"No snapshot element 585; call get_app_state"}],"nativeError":{"name":"Error","nativeCode":"stale_element"}}`),
		}},
	}
	got := []any{}
	for _, row := range rows {
		messages := []workerMessage{}
		w, err := newWorker(func(message workerMessage) error { messages = append(messages, message); return nil })
		if err != nil {
			t.Fatal(err)
		}
		if err = w.receive(context.Background(), workerMessage{Type: "init", Bootstrap: BootstrapSource()}); err != nil {
			t.Fatal(err)
		}
		observations := []any{}
		for index, code := range row.Cells {
			messages = nil
			raw, _ := json.Marshal(code)
			if err = w.receive(context.Background(), workerMessage{Type: "run", CellID: int64(index + 1), Code: raw}); err != nil {
				t.Fatal(err)
			}
			cursor := 0
			deadline := time.Now().Add(5 * time.Second)
			for w.active != nil && time.Now().Before(deadline) {
				for cursor < len(messages) {
					message := messages[cursor]
					cursor++
					if message.Type == "invoke" {
						result := row.NativeResult
						if selected := row.NativeResults[message.Name]; selected != nil {
							result = selected
						}
						if message.Name == "click" && bytes.Contains(message.Args, []byte(`"x"`)) && row.NativeResults != nil {
							result = json.RawMessage(`{"content":[]}`)
						}
						if result == nil {
							result = json.RawMessage(`{"content":[{"type":"text","text":"native result"}]}`)
						}
						if err = w.receive(context.Background(), workerMessage{Type: "response", CellID: message.CellID, RequestID: message.RequestID, Result: result}); err != nil {
							t.Fatal(err)
						}
					}
				}
				if err = w.fireTimers(); err != nil {
					t.Fatal(err)
				}
			}
			if w.active != nil {
				t.Fatal("worker did not settle", code)
			}
			observations = append(observations, compilerDecoded(messages))
		}
		w.owner.Close()
		got = append(got, observations)
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		var err error
		bun, err = exec.LookPath("bun")
		if err != nil {
			t.Skip("Source Bun unavailable")
		}
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	for _, path := range []string{home, filepath.Join(home, ".claude"), filepath.Join(home, "tmp")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cu-repl-worker.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", "run", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "BUN_OPTIONS=--no-env-file", "NODE_ENV=test", "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "CC_HAHA_CU_WORKER_FIXTURE=1"}
	input, _ := json.Marshal(map[string]any{"cases": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source worker %v %s", err, stderr.String())
	}
	var want []any
	if err = json.Unmarshal(output, &want); err != nil {
		t.Fatal(err, string(output))
	}
	if !reflect.DeepEqual(got, want) {
		for index := range got {
			if !reflect.DeepEqual(got[index], want[index]) {
				a, _ := json.Marshal(got[index])
				b, _ := json.Marshal(want[index])
				t.Errorf("case %d cells %v\nGo %s\nSource %s", index, rows[index].Cells, a, b)
			}
		}
	}
}
