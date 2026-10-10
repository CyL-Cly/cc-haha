package sessioncontroller

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func desktopOriginals(t *testing.T) []LiveRecord {
	t.Helper()
	inputs := []string{
		`{"type":"attachment","uuid":"file","unknown":{"keep":true},"attachment":{"type":"file","filename":"private.txt","content":{"type":"text","text":"retained file"},"future":17}}`,
		`{"type":"attachment","uuid":"directory","isVirtual":true,"attachment":{"type":"directory","path":"private-dir","content":"retained directory"}}`,
		`{"type":"attachment","uuid":"pdf","attachment":{"type":"pdf_reference","filename":"private.pdf","future":false}}`,
		`{"type":"attachment","uuid":"hidden","attachment":{"type":"hook_additional_context","content":"never saved"}}`,
		`{"type":"attachment","uuid":"hidden-case","attachment":{"type":"File","content":"never saved"}}`,
		`{"type":"progress","uuid":"progress","data":"never saved"}`,
		`{"type":"user","uuid":"user","message":{"role":"user","content":"original prompt"}}`,
		`{"type":"assistant","uuid":"assistant","isVirtual":true,"message":{"role":"assistant","content":[{"type":"tool_use","id":"r","name":"REPL","input":{}},{"type":"text","text":"visible text"},{"type":"tool_use","id":"w","name":"Write","input":{"file_path":"fixture"}}]}}`,
		`{"type":"user","uuid":"result","sourceToolAssistantUUID":"assistant","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"r","content":"hidden REPL"},{"type":"tool_result","tool_use_id":"w","content":"visible result"}]}}`,
	}
	messages := make([]LiveRecord, 0, len(inputs))
	for _, input := range inputs {
		messages = append(messages, LiveRecord{Record: toolRecordObject(t, input)})
	}
	return messages
}

func TestDesktopTranscriptNativeWritersShareAttachmentPolicyAndParentChain(t *testing.T) {
	for _, mode := range []string{"Record", "RecordLive", "BeginRecordLive"} {
		t.Run(mode, func(t *testing.T) {
			owner, w, p, loader := recordHost(t, OSFiles{}, nil)
			pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
				w.options.Environment["CC_HAHA_TRANSCRIPT_ENTRYPOINT"] = " \uFEFFclaude-desktop\u00A0 "
				return nil
			})
			messages := desktopOriginals(t)
			record := func(values []LiveRecord) json.RawMessage {
				t.Helper()
				if mode == "BeginRecordLive" {
					id, e := startRecord(t, owner, p, loader, values, RecordOptions{PromptID: func() json.RawMessage { return quote("turn") }, AllMessages: messages}).Wait(context.Background())
					if e != nil {
						t.Fatal(e)
					}
					return id
				}
				var id json.RawMessage
				pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
					var e error
					if mode == "RecordLive" {
						id, e = w.RecordLive(LiveRecordProfile{QuerySource: "sdk"}, values, quote("turn"))
					} else {
						records := make([]*protocol.JSONObject, 0, len(values))
						for _, v := range values {
							records = append(records, v.Record)
						}
						id, e = w.Record(records, quote("turn"))
					}
					return e
				})
				return id
			}
			if id := record(messages[:1]); text(id) != "file" {
				t.Fatalf("buffered attachment parent=%s", id)
			}
			if e := flushRecord(t, owner, p).Wait(context.Background()); e != nil {
				t.Fatal(e)
			}
			if _, e := os.Stat(w.target()); !os.IsNotExist(e) {
				t.Fatalf("attachment-only materialized history: %v", e)
			}
			if id := record(messages); text(id) != "result" {
				t.Fatalf("final parent=%s", id)
			}
			if id := record(messages); text(id) != "result" {
				t.Fatalf("dedup parent=%s", id)
			}
			if e := flushRecord(t, owner, p).Wait(context.Background()); e != nil {
				t.Fatal(e)
			}
			rows := []protocol.JSONObject{}
			for _, line := range strings.Split(strings.TrimSpace(recordBytes(t, w)), "\n") {
				var row protocol.JSONObject
				if e := row.UnmarshalJSON([]byte(line)); e != nil {
					t.Fatal(e)
				}
				if row.Get("uuid") != nil {
					rows = append(rows, row)
				}
			}
			ids := []string{"file", "directory", "pdf", "user", "assistant", "result"}
			if len(rows) != len(ids) {
				t.Fatalf("rows=%s", recordBytes(t, w))
			}
			for i, id := range ids {
				if rows[i].String("uuid") != id || rows[i].String("entrypoint") != "claude-desktop" {
					t.Fatalf("row %d=%s", i, recordBytes(t, w))
				}
				parent := ""
				if i > 0 {
					parent = ids[i-1]
				}
				if rows[i].String("parentUuid") != parent {
					t.Fatalf("row %s parent=%s", id, rows[i].Get("parentUuid"))
				}
			}
			if strings.Contains(recordBytes(t, w), `"isVirtual"`) || strings.Contains(recordBytes(t, w), "never saved") || strings.Contains(recordBytes(t, w), "hidden REPL") {
				t.Fatal(recordBytes(t, w))
			}
			// Read the physical file through a new real history loader, with
			// none of the original live records or writer caches supplied.
			restarted := recordLoader(t, nil)
			transcript, e := restarted.LoadTranscriptFile(w.target(), true)
			if e != nil {
				t.Fatal(e)
			}
			_, restored, e := restarted.BuildConversationChain(transcript, quote("result"))
			if e != nil {
				t.Fatal(e)
			}
			if len(restored) != len(ids) {
				t.Fatalf("restart history=%s", restored)
			}
			for i, encoded := range restored {
				var record protocol.JSONObject
				if e := record.UnmarshalJSON(encoded); e != nil {
					t.Fatal(e)
				}
				if record.String("uuid") != ids[i] {
					t.Fatalf("restart chain=%s", restored)
				}
			}
			attachment, _ := rows[0].Object("attachment")
			if attachment.String("filename") != "private.txt" || string(attachment.Get("future")) != "17" {
				t.Fatal("attachment lost unknown original fields")
			}
		})
	}
}

func TestDesktopTranscriptControllerAdmitsDesktopButKeepsAntAndHookGuards(t *testing.T) {
	home := t.TempDir()
	options := hostOptions(t, home).options
	options.WriterOptions.Environment["CC_HAHA_TRANSCRIPT_ENTRYPOINT"] = "claude-desktop"
	controller, e := New(options)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(controller.AwaitBackground)
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Fatal("admission touched files")
	}
	for _, name := range []string{"ant", "hook"} {
		t.Run(name, func(t *testing.T) {
			h := t.TempDir()
			o := hostOptions(t, h).options
			o.WriterOptions.Environment["CC_HAHA_TRANSCRIPT_ENTRYPOINT"] = "claude-desktop"
			if name == "ant" {
				o.ResumeOptions.Environment["USER_TYPE"] = "ant"
			} else {
				o.ResumeOptions.Environment["CLAUDE_CODE_SAVE_HOOK_ADDITIONAL_CONTEXT"] = "true"
			}
			if _, e := New(o); e == nil {
				t.Fatal("unported transcript policy admitted")
			}
		})
	}
}

func runDesktopTranscriptSource(t *testing.T, input any, script string) []byte {
	t.Helper()
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source requires pinned Bun")
	}
	root, e := filepath.Abs("../../..")
	if e != nil {
		t.Fatal(e)
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	tmp := filepath.Join(home, "tmp")
	for _, dir := range []string{config, tmp} {
		if e := os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	file := filepath.Join(home, "source.ts")
	if e := os.WriteFile(file, []byte(script), 0600); e != nil {
		t.Fatal(e)
	}
	encoded, e := json.Marshal(input)
	if e != nil {
		t.Fatal(e)
	}
	command := exec.Command(bun, file, root)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + tmp, "XDG_CONFIG_HOME=" + filepath.Join(home, "xdg"), "NODE_ENV=test", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=" + os.Getenv("PATH")}
	command.Stdin = strings.NewReader(string(encoded))
	output, e := command.CombinedOutput()
	if e != nil {
		t.Fatalf("Source %v: %s", e, output)
	}
	return output
}

func TestActualSourceDesktopTranscriptCleaningAndOriginalReferences(t *testing.T) {
	script := `globalThis.MACRO={VERSION:'fixture-version',BUILD_TIME:'',PACKAGE_URL:'',README_URL:'',ISSUES_URL:''};const root=Bun.argv[2];const input=await Bun.stdin.json();const storage=await import(root+'/src/utils/sessionStorage.ts');const output=[];for(const c of input){delete process.env.CC_HAHA_TRANSCRIPT_ENTRYPOINT;delete process.env.CLAUDE_CODE_ENTRYPOINT;Object.assign(process.env,c.env);const messages=c.messages;try{const loggable=messages.map(storage.isLoggableMessage);const cleaned=storage.cleanMessagesForLogging(messages,c.allMessages);const indexes=cleaned.map(m=>messages.indexOf(m));const attachmentShared=cleaned.map(m=>{const i=messages.findIndex(o=>o.uuid===m.uuid);return m.attachment===messages[i].attachment});const before=JSON.stringify(cleaned);for(const m of messages){if(m.attachment){m.attachment.future='later nested';m.lateOuter=true}}output.push({loggable,indexes,attachmentShared,before,after:JSON.stringify(cleaned)})}catch(e){output.push({error:{name:e.name,message:e.message}})}}console.log(JSON.stringify(output))`
	envs := []map[string]string{{}, {"CC_HAHA_TRANSCRIPT_ENTRYPOINT": "claude-desktop"}, {"CC_HAHA_TRANSCRIPT_ENTRYPOINT": " \uFEFFclaude-desktop\u00A0 "}, {"CLAUDE_CODE_ENTRYPOINT": "claude-desktop"}, {"CC_HAHA_TRANSCRIPT_ENTRYPOINT": " \t ", "CLAUDE_CODE_ENTRYPOINT": "claude-desktop"}, {"CC_HAHA_TRANSCRIPT_ENTRYPOINT": "cli", "CLAUDE_CODE_ENTRYPOINT": "claude-desktop"}, {"CLAUDE_CODE_ENTRYPOINT": " claude-desktop "}}
	cases := []any{}
	for _, env := range envs {
		originals := desktopOriginals(t)
		raw := []protocol.JSONObject{}
		for _, m := range originals {
			raw = append(raw, *m.Record)
		}
		cases = append(cases, map[string]any{"env": env, "messages": raw})
	}
	output := runDesktopTranscriptSource(t, cases, script)
	var expected []map[string]json.RawMessage
	if e := json.Unmarshal(output, &expected); e != nil {
		t.Fatalf("Source %v: %s", e, output)
	}
	for i, env := range envs {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			originals := desktopOriginals(t)
			values, e := cleanRecordForEnvironment(originals, nil, env)
			if e != nil {
				t.Fatal(e)
			}
			actual := map[string]any{}
			loggable := []bool{}
			for _, m := range originals {
				keep, e := isLoggableRecord(*m.Record, env)
				if e != nil {
					t.Fatal(e)
				}
				loggable = append(loggable, keep)
			}
			actual["loggable"] = loggable
			indexes := []int{}
			shared := []bool{}
			records := []protocol.JSONObject{}
			for _, v := range values {
				index := -1
				for j, m := range originals {
					if v.record == m.Record {
						index = j
					}
				}
				indexes = append(indexes, index)
				source := originals[0].Record
				for _, m := range originals {
					if m.Record.String("uuid") == v.record.String("uuid") {
						source = m.Record
						break
					}
				}
				child, ok := v.record.Object("attachment")
				parent, present := source.Object("attachment")
				shared = append(shared, !ok && !present || ok && present && child == parent)
				records = append(records, *v.record)
			}
			actual["indexes"] = indexes
			actual["attachmentShared"] = shared
			before, _ := json.Marshal(records)
			actual["before"] = string(before)
			for _, m := range originals {
				if a, ok := m.Record.Object("attachment"); ok {
					a.Set("future", quote("later nested"))
					m.Record.Set("lateOuter", raw(true))
				}
			}
			after, _ := json.Marshal(records)
			actual["after"] = string(after)
			encoded, _ := json.Marshal(actual)
			var got any
			var want any
			if e := json.Unmarshal(encoded, &got); e != nil {
				t.Fatal(e)
			}
			ex, _ := json.Marshal(expected[i])
			_ = json.Unmarshal(ex, &want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Source=%s\nGo=%s", ex, encoded)
			}
		})
	}
}

func TestActualSourceDesktopTranscriptInvalidAttachmentsAndFilterOrder(t *testing.T) {
	type input struct {
		Env         map[string]string `json:"env"`
		Messages    []json.RawMessage `json:"messages"`
		AllMessages []json.RawMessage `json:"allMessages"`
	}
	inputs := []input{}
	for _, env := range []map[string]string{{}, {"CC_HAHA_TRANSCRIPT_ENTRYPOINT": "claude-desktop"}} {
		for _, attachment := range []string{"", `,"attachment":null`, `,"attachment":false`, `,"attachment":[]`, `,"attachment":{"type":"future"}`} {
			m := json.RawMessage(`{"type":"attachment","uuid":"invalid"` + attachment + `}`)
			inputs = append(inputs, input{env, []json.RawMessage{m}, nil})
			inputs = append(inputs, input{env, []json.RawMessage{m}, []json.RawMessage{json.RawMessage(`{"type":"assistant","uuid":"bad-all","message":{"role":"assistant","content":[null]}}`)}})
		}
	}
	script := `globalThis.MACRO={VERSION:'fixture-version',BUILD_TIME:'',PACKAGE_URL:'',README_URL:'',ISSUES_URL:''};const root=Bun.argv[2];const inputs=await Bun.stdin.json();const storage=await import(root+'/src/utils/sessionStorage.ts');console.log(JSON.stringify(inputs.map(c=>{delete process.env.CC_HAHA_TRANSCRIPT_ENTRYPOINT;Object.assign(process.env,c.env);try{return {messages:storage.cleanMessagesForLogging(c.messages,c.allMessages??c.messages)}}catch(e){return {error:{name:e.name,message:e.message}}}})))`
	output := runDesktopTranscriptSource(t, inputs, script)
	var expected []any
	if e := json.Unmarshal(output, &expected); e != nil {
		t.Fatalf("Source %v=%s", e, output)
	}
	for i, input := range inputs {
		decode := func(raw []json.RawMessage) []LiveRecord {
			if raw == nil {
				return nil
			}
			v := []LiveRecord{}
			for _, r := range raw {
				v = append(v, LiveRecord{Record: toolRecordObject(t, string(r))})
			}
			return v
		}
		cleaned, e := cleanRecordForEnvironment(decode(input.Messages), decode(input.AllMessages), input.Env)
		actual := map[string]any{}
		if e != nil {
			named, ok := e.(interface{ ErrorName() string })
			if !ok {
				t.Fatalf("case %d unsupported=%v", i, e)
			}
			actual["error"] = map[string]any{"name": named.ErrorName(), "message": e.Error()}
		} else {
			messages := []protocol.JSONObject{}
			for _, v := range cleaned {
				messages = append(messages, *v.record)
			}
			actual["messages"] = messages
		}
		raw, _ := json.Marshal(actual)
		var got any
		_ = json.Unmarshal(raw, &got)
		if !reflect.DeepEqual(got, expected[i]) {
			t.Fatalf("case %d Source=%v Go=%s", i, expected[i], raw)
		}
	}
}

func TestActualSourceDesktopTranscriptPhysicalChainAndNativeReload(t *testing.T) {
	messages := desktopOriginals(t)
	inputs := []protocol.JSONObject{}
	for _, m := range messages {
		inputs = append(inputs, *m.Record)
	}
	script := `import * as fs from 'node:fs';import {join} from 'node:path';globalThis.MACRO={VERSION:'fixture-version',BUILD_TIME:'',PACKAGE_URL:'',README_URL:'',ISSUES_URL:''};const root=Bun.argv[2];const messages=await Bun.stdin.json();const home=process.env.HOME;process.env.TEST_ENABLE_SESSION_PERSISTENCE='1';process.env.CC_HAHA_TRANSCRIPT_ENTRYPOINT=' \uFEFFclaude-desktop\u00A0 ';const state=await import(root+'/src/bootstrap/state.ts');state.setCwdState(home);state.setOriginalCwd(home);state.setProjectRoot(home);state.setIsInteractive(false);state.switchSession('11111111-1111-4111-8111-111111111111');const cache=await import(root+'/src/utils/settings/settingsCache.ts');cache.setSessionSettingsCache({settings:{},errors:[]});const config=await import(root+'/src/utils/config.ts');config.enableConfigs();const storage=await import(root+'/src/utils/sessionStorage.ts');const path=join(process.env.CLAUDE_CONFIG_DIR,'projects',home.replace(/[^a-zA-Z0-9]/g,'-'),'11111111-1111-4111-8111-111111111111.jsonl');const ids=[];ids.push(await storage.recordTranscript(messages.slice(0,1)));await storage.flushSessionStorage();const attachmentOnlyFile=fs.existsSync(path);ids.push(await storage.recordTranscript(messages));ids.push(await storage.recordTranscript(messages));await storage.flushSessionStorage();const loaded=await storage.loadTranscriptFile(path);const chain=storage.buildConversationChain(loaded.messages,loaded.messages.get('result'));const project=m=>({uuid:m.uuid,parentUuid:m.parentUuid,entrypoint:m.entrypoint,...(m.attachment===undefined?{}:{attachment:m.attachment}),...(m.message===undefined?{}:{message:m.message}),...(m.unknown===undefined?{}:{unknown:m.unknown}),...(m.isVirtual===undefined?{}:{isVirtual:m.isVirtual})});console.log(JSON.stringify({ids,attachmentOnlyFile,rows:fs.readFileSync(path,'utf8').trim().split('\n').map(x=>JSON.parse(x)).filter(m=>m.uuid).map(project),chain:chain.map(project)}))`
	output := runDesktopTranscriptSource(t, inputs, script)
	var expected any
	if e := json.Unmarshal(output, &expected); e != nil {
		t.Fatalf("Source %v=%s", e, output)
	}
	owner, w, p, loader := recordHost(t, OSFiles{}, nil)
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
		w.options.Environment["CC_HAHA_TRANSCRIPT_ENTRYPOINT"] = " \uFEFFclaude-desktop\u00A0 "
		return nil
	})
	ids := []string{}
	record := func(m []LiveRecord) {
		id, e := startRecord(t, owner, p, loader, m, RecordOptions{PromptID: func() json.RawMessage { return nil }}).Wait(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, text(id))
	}
	record(messages[:1])
	if e := flushRecord(t, owner, p).Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	_, statError := os.Stat(w.target())
	record(messages)
	record(messages)
	if e := flushRecord(t, owner, p).Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	project := func(m protocol.JSONObject) map[string]json.RawMessage {
		p := map[string]json.RawMessage{}
		for _, name := range []string{"uuid", "parentUuid", "entrypoint", "attachment", "message", "unknown", "isVirtual"} {
			if value := m.Get(name); value != nil {
				p[name] = value
			}
		}
		return p
	}
	rows := []map[string]json.RawMessage{}
	for _, line := range strings.Split(strings.TrimSpace(recordBytes(t, w)), "\n") {
		var m protocol.JSONObject
		if e := m.UnmarshalJSON([]byte(line)); e != nil {
			t.Fatal(e)
		}
		if m.Get("uuid") != nil {
			rows = append(rows, project(m))
		}
	}
	restarted := recordLoader(t, nil)
	transcript, e := restarted.LoadTranscriptFile(w.target(), true)
	if e != nil {
		t.Fatal(e)
	}
	raw, _, e := restarted.BuildConversationChain(transcript, quote("result"))
	if e != nil {
		t.Fatal(e)
	}
	chain := []map[string]json.RawMessage{}
	for _, encoded := range raw {
		var m protocol.JSONObject
		_ = m.UnmarshalJSON(encoded)
		chain = append(chain, project(m))
	}
	actual := map[string]any{"ids": ids, "attachmentOnlyFile": statError == nil, "rows": rows, "chain": chain}
	encoded, e := json.Marshal(actual)
	if e != nil {
		t.Fatal(e)
	}
	var got any
	_ = json.Unmarshal(encoded, &got)
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("Source=%s\nGo=%s", output, encoded)
	}
}

func TestDesktopTranscriptMalformedAttachmentRejectsIndependentPromiseBeforeIO(t *testing.T) {
	owner, w, p, loader := recordHost(t, OSFiles{}, nil)
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
		w.options.Environment["CC_HAHA_TRANSCRIPT_ENTRYPOINT"] = "claude-desktop"
		return nil
	})
	message := toolRecordObject(t, `{"type":"attachment","uuid":"bad","attachment":null}`)
	var previous *LoggingTypeError
	for i := 0; i < 2; i++ {
		future := startRecord(t, owner, p, loader, []LiveRecord{{Record: message}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
		if future == nil {
			t.Fatal("async Source cleaning rejection became constructor refusal")
		}
		_, e := future.Wait(context.Background())
		typed, ok := e.(*LoggingTypeError)
		if !ok || typed.ErrorName() != "TypeError" {
			t.Fatalf("promise error=%T %v", e, e)
		}
		if typed == previous {
			t.Fatal("new cleaning call reused the error identity")
		}
		previous = typed
	}
	if _, e := os.Stat(w.target()); !os.IsNotExist(e) {
		t.Fatalf("rejected original promise wrote history %v", e)
	}
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		if p.tracked != 0 || w.path != "" || len(w.pending) > 0 || len(w.queues) > 0 {
			t.Fatal("rejected cleaning admitted native writes")
		}
		return nil
	})
}
