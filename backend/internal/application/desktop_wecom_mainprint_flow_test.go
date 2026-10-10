package application

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
	"golang.org/x/net/websocket"
)

func TestActualDesktopWecomLauncherMainPrintPermissionWriteReadDedupColdResumeAndClose(t *testing.T) {
	actualWecomMainprint(t, "")
}
func TestActualWecomOfficialDeepSeekSharedMainPrintLiveModel(t *testing.T) {
	key := os.Getenv("CC_HAHA_WECOM_LIVE_TOKEN")
	if key == "" {
		t.Skip("separate maintainer-authorized official model evidence")
	}
	if os.Getenv("CC_HAHA_WECOM_LIVE_GATE") != "1" {
		t.Fatal("live gate is not authorized")
	}
	actualWecomMainprint(t, key)
}

func wecomFlowObject(data []byte) protocol.JSONObject {
	value := protocol.NewJSONObject()
	_ = value.UnmarshalJSON(data)
	return value
}
func wecomFlowField(name string, value any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: raw(value)}
}
func wecomFlowMessage(id, reqID, text string) protocol.JSONObject {
	return protocol.NewJSONObject(wecomFlowField("cmd", "aibot_msg_callback"), wecomFlowField("headers", map[string]string{"req_id": reqID}), wecomFlowField("body", map[string]any{"msgid": id, "chattype": "single", "msgtype": "text", "from": map[string]string{"userid": "U1"}, "text": map[string]string{"content": text}}))
}
func wecomFlowACK(conn *websocket.Conn, frame protocol.JSONObject) error {
	return websocket.Message.Send(conn, string(raw(protocol.NewJSONObject(wecomFlowField("headers", wecomFlowObject(frame.Get("headers"))), wecomFlowField("errcode", 0)))))
}

// This model double supplies only Anthropic responses. All platform frames,
// permission inputs, tools, persistence, sessions and Server/CLI dispatch are real.
func wecomFlowFirstModelResponse(w http.ResponseWriter, target string) {
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(kind string, value any) { _, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, raw(value)) }
	emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "wecom-write", "type": "message", "role": "assistant", "content": []any{}, "model": "fixture-model", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 12, "output_tokens": 1, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}}})
	emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
	emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "WECOM_SHARED_PREFACE"}})
	emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	emit("content_block_start", map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "tool_use", "id": "wecom-write-tool", "name": "Write", "input": map[string]any{}}})
	emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(raw(map[string]string{"file_path": target, "content": "WECOM_SHARED_MAINPRINT_FILE\n"}))}})
	emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 1})
	emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use", "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 8}})
	emit("message_stop", map[string]any{"type": "message_stop"})
}

func TestActualSourceWecomFreshClearDedupBeforeResponseAndCapturedAnchor(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("explicit pinned Source Bun required")
	}
	home := t.TempDir()
	config := filepath.Join(home, "config")
	if e := os.MkdirAll(config, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(config, "adapters.json"), []byte(`{"wecom":{"allowedUsers":["U1"]}}`), 0600); e != nil {
		t.Fatal(e)
	}
	root, e := filepath.Abs("../../..")
	if e != nil {
		t.Fatal(e)
	}
	// Original class, original inbound event listener, and original shared
	// runtime execute unchanged. Only HTTP/bridge/SDK wire producer seams are
	// instrumented here; the other test exercises their actual physical chain.
	code := `
import {readFileSync} from 'node:fs'; import {join} from 'node:path'; import {pathToFileURL} from 'node:url';
const base=process.env.CC_HAHA_WECOM_SOURCE_ROOT;
const load=p=>import(pathToFileURL(join(base,p)).href);
const {ImChatRuntime}=await load('adapters/common/chat-runtime.ts');
const {MessageDedup}=await load('adapters/common/message-dedup.ts');
const {extractWecomPayload}=await load('adapters/wecom/extract-payload.ts');
const {collectWecomMediaCandidates}=await load('adapters/wecom/media.ts');
const {splitMessageByBytes,utf8Length}=await load('adapters/common/format.ts');
const {WSClient}=await load('adapters/node_modules/@wecom/aibot-node-sdk/dist/index.esm.js');
const source=readFileSync(join(base,'adapters/wecom/index.ts'),'utf8');
const transpile=text=>new Bun.Transpiler({loader:'ts'}).transformSync(text);
const logger={debug(){},info(){},warn(){},error(){}};
const client=new WSClient({botId:'private',secret:'private',logger});
const replyFrames=new Map(); const calls=[]; let duplicateSent=false; let consume;
const frame=req=>({cmd:'aibot_msg_callback',headers:{req_id:req},body:{msgid:'first',msgtype:'text',chattype:'single',from:{userid:'U1'},text:{content:'prompt'}}});
client.wsManager.sendReply=async(reqId,body,cmd='aibot_respond_msg')=>{calls.push({cmd,...(cmd==='aibot_respond_msg'?{reqId}:{}),body});if(cmd==='aibot_respond_msg'&&!duplicateSent){duplicateSent=true;client.wsManager.onMessage(frame('later-duplicate'))}return{errcode:0}};
const markdownStart=source.indexOf('async function sendMarkdown('),markdownEnd=source.indexOf('\n/**',markdownStart);
const sendMarkdown=new Function('splitMessageByBytes','WECOM_TEXT_BYTE_LIMIT','client',transpile(source.slice(markdownStart,markdownEnd))+';return sendMarkdown')(splitMessageByBytes,4000,client);
const start=source.indexOf('class WecomResponse implements ResponseStream'),end=source.indexOf('\nconst port:',start);
const Response=new Function('crypto','replyFrames','client','sendMarkdown','utf8Length','WECOM_STREAM_BYTE_LIMIT','console',transpile(source.slice(start,end))+';return WecomResponse')({randomUUID:()=> 'fixed'},replyFrames,client,sendMarkdown,utf8Length,19000,logger);
const dedup=new MessageDedup(); const bindings=new Map();let sent=0,cleared=0,initialClearRemoved=false,resolve;
const admitted=new Promise(r=>resolve=r);
const bridge={hasSession(){return false},resetSession(){},connectSession(){},onServerMessage(){},waitForOpen:async()=>true,sendUserMessage(){sent++;resolve();return true}};
const runtime=new ImChatRuntime({port:{platform:'wecom',logPrefix:'[WeCom]',sendNotice:sendMarkdown,createResponse:id=>new Response(id),clearChat(id){cleared++;initialClearRemoved=replyFrames.has(id);replyFrames.delete(id)}},config:{},platformConfig:{},bridge,sessionStore:{get:id=>bindings.get(id),set(id,sessionId,workDir){bindings.set(id,{sessionId,workDir})}},httpClient:{createSession:async()=> '00000000-0000-4000-8000-000000000001'},defaultWorkDir:process.env.HOME,dedup,flushIntervalMs:500,flushCharThreshold:160});
const listenerStart=source.indexOf("client.on('message', (frame"),listenerEnd=source.indexOf("\nclient.on('authenticated'",listenerStart);
new Function('client','extractWecomPayload','replyFrames','collectWecomMediaCandidates','runtime','collectAttachments',transpile(source.slice(listenerStart,listenerEnd)))(client,extractWecomPayload,replyFrames,collectWecomMediaCandidates,runtime,async()=>[]);
client.wsManager.onMessage(frame('inbound-first'));await admitted;
const afterFreshClear=replyFrames.has('U1');client.wsManager.onMessage(frame('model-ready-duplicate'));
await runtime.handleServerMessage('U1',{type:'content_delta',text:'WECOM_SHARED_PREFACE'});
await runtime.handleServerMessage('U1',{type:'permission_request',requestId:'permission',toolName:'Write',input:{file_path:'/private/file',content:'x'}});
dedup.destroy();process.stdout.write(JSON.stringify({sent,cleared,initialClearRemoved,afterFreshClear,streams:calls.filter(c=>c.cmd==='aibot_respond_msg').map(c=>({reqId:c.reqId,text:c.body.stream.content,finish:c.body.stream.finish}))}));
`
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", "--eval", code)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_WECOM_SOURCE_ROOT=" + root}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatal("actual Source caller", e, stderr.String())
	}
	got := wecomFlowObject(data)
	if string(got.Get("sent")) != "1" || string(got.Get("cleared")) != "1" || string(got.Get("initialClearRemoved")) != "true" || string(got.Get("afterFreshClear")) != "false" {
		t.Fatal("original createSession/duplicate authority changed", string(data))
	}
	var streams []protocol.JSONObject
	if e := json.Unmarshal(got.Get("streams"), &streams); e != nil || len(streams) != 2 {
		t.Fatal("original response trace missing", e, string(data))
	}
	for _, stream := range streams {
		if stream.String("reqId") != "model-ready-duplicate" || stream.String("text") != "WECOM_SHARED_PREFACE" {
			t.Fatal("original response did not keep captured model-ready frame", string(data))
		}
	}
	if string(streams[0].Get("finish")) != "false" || string(streams[1].Get("finish")) != "true" {
		t.Fatal("Source final frame ordering changed", string(data))
	}
}

func actualWecomMainprint(t *testing.T, liveKey string) {
	t.Helper()
	var fakeRequests atomic.Int32
	var catalogMu sync.Mutex
	catalogs := [][]string{}
	target := ""
	modelReadyDuplicate := func() error { return fmt.Errorf("private platform not started") }
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var packet struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(body, &packet); err != nil {
			t.Error("invalid actual MainPrint POST")
		}
		names := []string{}
		for _, tool := range packet.Tools {
			names = append(names, tool.Name)
		}
		catalogMu.Lock()
		catalogs = append(catalogs, names)
		catalogMu.Unlock()
		switch fakeRequests.Add(1) {
		case 1:
			if e := modelReadyDuplicate(); e != nil {
				t.Error("actual model/input delivery barrier", e)
				http.Error(w, "delivery barrier", 500)
				return
			}
			wecomFlowFirstModelResponse(w, target)
		case 2:
			response(w, "wecom-read", "Read", map[string]string{"file_path": target}, "")
		case 3:
			response(w, "wecom-final", "", nil, "WECOM_SHARED_MAINPRINT_DONE")
		case 4:
			response(w, "wecom-cold-read", "Read", map[string]string{"file_path": target}, "")
		case 5:
			response(w, "wecom-cold-final", "", nil, "WECOM_SHARED_MAINPRINT_RESUMED")
		default:
			t.Error("extra/duplicate model turn")
			http.Error(w, "unexpected model request", 500)
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Arguments.PermissionMode = "default"
	o.Arguments.Tools = nil
	o.Arguments.SystemPrompt = ""
	o.Environment["ENABLE_TOOL_SEARCH"] = "false"
	o.Environment["DISABLE_TELEMETRY"] = "1"
	o.Environment["CC_HAHA_LOCAL_ACCESS_TOKEN"] = "private-wecom-server-token"
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	target = filepath.Join(o.WorkDir, "wecom-mainprint-file.txt")
	if err := os.MkdirAll(filepath.Join(config, "cc-haha"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "adapters.json"), []byte(`{"future":{"preserve":true},"wecom":{"futurePlatform":{"keep":true}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if liveKey != "" {
		o.Arguments.Model = "deepseek-flash[1m]"
		o.Arguments.MaxTurns = 8
		o.Environment["CC_HAHA_TRACE_API_CALLS"] = "1"
		delete(o.Environment, "ANTHROPIC_API_KEY")
		delete(o.Environment, "ANTHROPIC_BASE_URL")
		provider := map[string]any{"id": "wecom-official-validation", "presetId": "deepseek", "name": "Private WeCom official model", "baseUrl": "https://api.deepseek.com/anthropic", "apiKey": liveKey, "authStrategy": "auth_token", "apiFormat": "anthropic", "supportsNestedToolResultMedia": false, "toolSearchEnabled": false, "models": map[string]string{"main": "deepseek-flash[1m]", "haiku": "deepseek-flash[1m]", "sonnet": "deepseek-flash[1m]", "opus": "deepseek-flash[1m]"}, "modelContextWindows": map[string]int{"deepseek-flash": 1000000}, "autoCompactWindow": 1000000}
		if err := os.WriteFile(filepath.Join(config, "cc-haha", "providers.json"), raw(map[string]any{"schemaVersion": 6, "activeId": "wecom-official-validation", "providers": []any{provider}, "providerOrder": []string{"wecom-official-validation"}}), 0600); err != nil {
			t.Fatal(err)
		}
	}
	startDesktop := func() (*server.Server, *httptest.Server) {
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		actual, e := NewDesktopServer(ServerOptions{Port: listener.Addr().(*net.TCPAddr).Port, Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
			return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
		}})
		if e != nil {
			_ = listener.Close()
			t.Fatal(e)
		}
		httpServer := httptest.NewUnstartedServer(actual)
		_ = httpServer.Listener.Close()
		httpServer.Listener = listener
		httpServer.Start()
		return actual, httpServer
	}
	actual, desktop := startDesktop()
	defer func() { _ = actual.Close(context.Background()); desktop.Close() }()
	patch := map[string]any{"defaultProjectDir": o.WorkDir, "allowedProjectRoots": []string{o.WorkDir}, "wecom": map[string]any{"botId": "private-wecom-bot", "secret": "private-platform-secret", "allowedUsers": []string{"U1"}}}
	req, _ := http.NewRequest("PUT", desktop.URL+"/api/adapters", bytes.NewReader(raw(patch)))
	req.Header.Set("Authorization", "Bearer private-wecom-server-token")
	req.Header.Set("Content-Type", "application/json")
	saved, e := desktop.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	savedBytes, _ := io.ReadAll(saved.Body)
	_ = saved.Body.Close()
	if saved.StatusCode != 200 || !bytes.Contains(savedBytes, []byte(`"preserve":true`)) || !bytes.Contains(savedBytes, []byte(`"keep":true`)) || bytes.Contains(savedBytes, []byte("private-platform-secret")) {
		t.Fatal("actual config consumer or secret masking failed", saved.StatusCode)
	}
	var platformMu sync.Mutex
	outbound := []protocol.JSONObject{}
	logs := []string{}
	var approvals, connections, acks atomic.Int32
	permissionPattern := regexp.MustCompile(`需要权限确认 \[([^\]]+)\]`)
	stage := atomic.Int32{}
	firstDone, resumeDone := make(chan struct{}), make(chan struct{})
	var firstOnce, resumeOnce, duplicateOnce sync.Once
	prompt := "Write the private target using Write, then use Read, then respond WECOM_SHARED_MAINPRINT_DONE."
	if liveKey != "" {
		prompt = fmt.Sprintf("Use Write to create %s with exact content WECOM_SHARED_MAINPRINT_FILE followed by a newline. Use Read to verify the same file. Then respond with only WECOM_SHARED_MAINPRINT_DONE. Do not use Bash or delegate.", target)
	}
	first := wecomFlowMessage("first", "inbound-first", prompt)
	var currentPeerMu sync.Mutex
	var currentPeer *websocket.Conn
	duplicateDelivered := make(chan struct{})
	var deliveredOnce sync.Once
	peer := httptest.NewTLSServer(websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
		defer conn.Close()
		currentPeerMu.Lock()
		currentPeer = conn
		currentPeerMu.Unlock()
		connections.Add(1)
		var data string
		if websocket.Message.Receive(conn, &data) != nil {
			return
		}
		auth := wecomFlowObject([]byte(data))
		credentials := wecomFlowObject(auth.Get("body"))
		if auth.String("cmd") != "aibot_subscribe" || credentials.String("bot_id") != "private-wecom-bot" || credentials.String("secret") != "private-platform-secret" {
			t.Error("actual launcher lost saved credentials")
			return
		}
		_ = wecomFlowACK(conn, auth)
		if stage.Load() == 0 {
			_ = websocket.Message.Send(conn, string(raw(first)))
			_ = websocket.Message.Send(conn, string(raw(first)))
		} else {
			resumePrompt := "Use Read on the existing private file and respond WECOM_SHARED_MAINPRINT_RESUMED."
			if liveKey != "" {
				resumePrompt = fmt.Sprintf("Use Read to verify %s still contains WECOM_SHARED_MAINPRINT_FILE followed by a newline. Do not rewrite it. Respond only WECOM_SHARED_MAINPRINT_RESUMED. Do not use Bash or delegate.", target)
			}
			message := wecomFlowMessage("resume", "inbound-resume", resumePrompt)
			_ = websocket.Message.Send(conn, string(raw(message)))
			_ = websocket.Message.Send(conn, string(raw(message)))
		}
		for {
			if websocket.Message.Receive(conn, &data) != nil {
				return
			}
			frame := wecomFlowObject([]byte(data))
			cmd := frame.String("cmd")
			if cmd == "ping" {
				_ = wecomFlowACK(conn, frame)
				continue
			}
			if cmd != "aibot_respond_msg" && cmd != "aibot_send_msg" {
				t.Error("unexpected actual platform command", cmd)
				return
			}
			platformMu.Lock()
			outbound = append(outbound, frame.Clone())
			platformMu.Unlock()
			body := wecomFlowObject(frame.Get("body"))
			text := ""
			finished := false
			if body.String("msgtype") == "markdown" {
				text = wecomFlowObject(body.Get("markdown")).String("content")
			} else if body.String("msgtype") == "stream" {
				stream := wecomFlowObject(body.Get("stream"))
				text = stream.String("content")
				finished = string(stream.Get("finish")) == "true"
			} else {
				t.Error("unexpected actual platform reply shape")
				return
			}
			if strings.Contains(text, "WECOM_SHARED_PREFACE") {
				duplicateOnce.Do(func() {
					different := first.Clone()
					different.Set("headers", raw(map[string]string{"req_id": "later-duplicate"}))
					_ = websocket.Message.Send(conn, string(raw(different)))
				})
			}
			_ = wecomFlowACK(conn, frame)
			acks.Add(1)
			if match := permissionPattern.FindStringSubmatch(text); len(match) > 1 {
				n := approvals.Add(1)
				approval := wecomFlowMessage(fmt.Sprintf("approval-%d", n), fmt.Sprintf("approval-anchor-%d", n), "/allow "+match[1])
				_ = websocket.Message.Send(conn, string(raw(approval)))
			}
			if finished && strings.TrimSpace(text) == "WECOM_SHARED_MAINPRINT_DONE" {
				firstOnce.Do(func() { close(firstDone) })
			}
			if finished && strings.TrimSpace(text) == "WECOM_SHARED_MAINPRINT_RESUMED" {
				resumeOnce.Do(func() { close(resumeDone) })
			}
		}
	}})
	defer peer.Close()
	modelReadyDuplicate = func() error {
		currentPeerMu.Lock()
		conn := currentPeer
		currentPeerMu.Unlock()
		if conn == nil {
			return fmt.Errorf("private platform not connected")
		}
		duplicate := first.Clone()
		duplicate.Set("headers", raw(map[string]string{"req_id": "model-ready-duplicate"}))
		if e := websocket.Message.Send(conn, string(raw(duplicate))); e != nil {
			return e
		}
		select {
		case <-duplicateDelivered:
			return nil
		case <-time.After(5 * time.Second):
			return fmt.Errorf("actual OnMessage did not deliver duplicate before first model response")
		}
	}
	roots := x509.NewCertPool()
	roots.AddCert(peer.Certificate())
	startAdapter := func(httpServer *httptest.Server) (*imruntime.WecomRuntime, context.CancelFunc, <-chan error) {
		env := map[string]string{}
		for key, value := range o.Environment {
			env[key] = value
		}
		env["ADAPTER_SERVER_URL"] = "ws" + strings.TrimPrefix(httpServer.URL, "http")
		ctx, cancel := context.WithCancel(context.Background())
		started := make(chan *imruntime.WecomRuntime, 1)
		done := make(chan error, 1)
		go func() {
			code, e := adapterlauncher.Run(ctx, []string{"--app-root", "/private/fixture/app.asar", "--wecom"}, adapterlauncher.Options{Environment: env, HomeDir: home, WorkDir: o.WorkDir, Stderr: io.Discard, StartWecom: func(ctx context.Context, options adapterlauncher.Options) (adapterlauncher.Process, error) {
				cfg := imruntime.LoadPlatformConfig("wecom", home, o.WorkDir, options.Environment, func(string) {})
				runtime, e := imruntime.NewWecomRuntime(ctx, imruntime.WecomRuntimeOptions{Config: cfg, ServerHTTP: httpServer.Client(), Log: func(value string) { platformMu.Lock(); logs = append(logs, value); platformMu.Unlock() }, NewClient: func(ctx context.Context, options imruntime.WecomClientOptions) (*imruntime.WecomClient, error) {
					consume := options.OnMessage
					options.OnMessage = func(frame protocol.JSONObject) {
						consume(frame)
						if wecomFlowObject(frame.Get("headers")).String("req_id") == "model-ready-duplicate" {
							deliveredOnce.Do(func() { close(duplicateDelivered) })
						}
					}
					return imruntime.NewWecomClient(ctx, options)
				}, ClientOptions: imruntime.WecomClientOptions{Dial: func(ctx context.Context, address string, _ *tls.Config) (*imruntime.Socket, error) {
					if address != imruntime.WecomWebSocketURL {
						return nil, fmt.Errorf("actual official endpoint changed")
					}
					return imruntime.DialSocket(ctx, "wss"+strings.TrimPrefix(peer.URL, "https"), &tls.Config{RootCAs: roots})
				}}})
				if e != nil {
					return nil, e
				}
				runtime.Start()
				started <- runtime
				return runtime, nil
			}})
			if e == nil && code != 0 {
				e = fmt.Errorf("launcher exit %d", code)
			}
			done <- e
		}()
		select {
		case runtime := <-started:
			return runtime, cancel, done
		case e := <-done:
			cancel()
			t.Fatal("public Go adapter launcher", e)
		case <-time.After(10 * time.Second):
			cancel()
			t.Fatal("adapter startup deadline")
		}
		return nil, cancel, done
	}
	waitDone := func(signal <-chan struct{}, launcher <-chan error, label string) {
		deadline := 30 * time.Second
		if liveKey != "" {
			deadline = 150 * time.Second
		}
		select {
		case <-signal:
		case e := <-launcher:
			t.Fatal(label, "launcher exited", e)
		case <-time.After(deadline):
			platformMu.Lock()
			snapshot := append([]string{}, logs...)
			replyCount := len(outbound)
			platformMu.Unlock()
			t.Fatal(label, "reply missing", fakeRequests.Load(), approvals.Load(), replyCount, snapshot)
		}
	}
	stopAdapter := func(cancel context.CancelFunc, done <-chan error) {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Fatal("adapter Close", e)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("adapter Close did not join")
		}
	}
	readCanonical := func(id string) []byte {
		var result []byte
		_ = filepath.WalkDir(filepath.Join(config, "projects"), func(path string, entry os.DirEntry, e error) error {
			if e == nil && !entry.IsDir() && entry.Name() == id+".jsonl" {
				result, _ = os.ReadFile(path)
			}
			return nil
		})
		return result
	}
	runtime, cancel, launcherDone := startAdapter(desktop)
	defer cancel()
	waitDone(firstDone, launcherDone, "first turn")
	file, e := os.ReadFile(target)
	if e != nil || string(file) != "WECOM_SHARED_MAINPRINT_FILE\n" || approvals.Load() != 1 {
		t.Fatal("actual Write/Read or permission failed", e, approvals.Load())
	}
	stored := runtime.Store.Get("U1")
	if stored == nil {
		t.Fatal("real platform session store missing")
	}
	sessionID := stored.SessionID
	stopAdapter(cancel, launcherDone)
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if e := actual.Close(closeCtx); e != nil {
		closeCancel()
		t.Fatal("first actual MainPrint Close", e)
	}
	closeCancel()
	desktop.Close()
	beforeCanonical := readCanonical(sessionID)
	if len(beforeCanonical) == 0 {
		t.Fatal("canonical transcript not persisted")
	}
	if liveKey == "" && fakeRequests.Load() != 3 {
		t.Fatal("duplicate frame created extra Harness requests", fakeRequests.Load())
	}
	if liveKey != "" && fakeRequests.Load() != 0 {
		t.Fatal("live model used loopback double")
	}
	stage.Store(1)
	actual, desktop = startDesktop()
	restarted, restartCancel, restartDone := startAdapter(desktop)
	defer restartCancel()
	waitDone(resumeDone, restartDone, "cold resume")
	binding := restarted.Store.Get("U1")
	if binding == nil || binding.SessionID != sessionID {
		t.Fatal("server+adapter cold restart changed actual SID")
	}
	if liveKey == "" && fakeRequests.Load() != 5 {
		t.Fatal("cold resume request count", fakeRequests.Load())
	}
	if liveKey != "" && fakeRequests.Load() != 0 {
		t.Fatal("cold live turn used fake model")
	}
	stopAdapter(restartCancel, restartDone)
	closeCtx, closeCancel = context.WithTimeout(context.Background(), 10*time.Second)
	if e := actual.Close(closeCtx); e != nil {
		closeCancel()
		t.Fatal("resumed MainPrint Close", e)
	}
	closeCancel()
	desktop.Close()
	canonical := readCanonical(sessionID)
	if len(canonical) <= len(beforeCanonical) || !bytes.Contains(canonical, []byte("WECOM_SHARED_MAINPRINT_RESUMED")) {
		t.Fatal("cold resume did not extend same real canonical")
	}
	for _, marker := range []string{`"name":"Write"`, `"name":"Read"`, "WECOM_SHARED_MAINPRINT_DONE"} {
		if !bytes.Contains(canonical, []byte(marker)) {
			t.Fatal("canonical lacks actual tool/result", marker)
		}
	}
	platformMu.Lock()
	replies := append([]protocol.JSONObject{}, outbound...)
	platformMu.Unlock()
	prefaceSeen, firstSeen, resumeSeen := false, false, false
	for _, frame := range replies {
		body := wecomFlowObject(frame.Get("body"))
		if body.String("msgtype") != "stream" {
			continue
		}
		stream := wecomFlowObject(body.Get("stream"))
		text := stream.String("content")
		anchor := wecomFlowObject(frame.Get("headers")).String("req_id")
		if strings.Contains(text, "WECOM_SHARED_PREFACE") {
			prefaceSeen = true
			if anchor != "model-ready-duplicate" {
				t.Fatal("existing response captured a later duplicate anchor", anchor)
			}
		}
		if text == "WECOM_SHARED_MAINPRINT_DONE" {
			firstSeen = true
			if anchor != "approval-anchor-1" {
				t.Fatal("post-approval response did not capture current Source anchor", anchor)
			}
		}
		if text == "WECOM_SHARED_MAINPRINT_RESUMED" {
			resumeSeen = true
			if anchor != "inbound-resume" {
				t.Fatal("cold response lost actual resume frame", anchor)
			}
		}
	}
	if liveKey == "" && !prefaceSeen {
		t.Fatal("actual native text/tool stream was not rendered", "canonicalHasPreface", bytes.Contains(canonical, []byte("WECOM_SHARED_PREFACE")), "actualPlatformReplies", string(raw(replies)))
	}
	if !firstSeen || !resumeSeen || acks.Load() == 0 || connections.Load() != 2 {
		t.Fatal("real platform reply/reconnect evidence missing", firstSeen, resumeSeen, acks.Load(), connections.Load())
	}
	if liveKey == "" {
		catalogMu.Lock()
		snapshots := append([][]string{}, catalogs...)
		catalogMu.Unlock()
		for _, names := range snapshots {
			for _, required := range []string{"Write", "Read", "Workflow", "Agent"} {
				found := false
				for _, name := range names {
					found = found || name == required
				}
				if !found {
					t.Fatal("ordinary default catalog was replaced", required)
				}
			}
		}
	}
	if liveKey != "" {
		if bytes.Contains(canonical, []byte(liveKey)) {
			t.Fatal("credential in canonical")
		}
		receiptRoot := os.Getenv("CC_HAHA_WECOM_LIVE_RECEIPT_ROOT")
		if !filepath.IsAbs(receiptRoot) {
			t.Fatal("absolute private receipt root required")
		}
		receipt := map[string]any{"status": "passed", "scope": "actual desktop PUT + Go adapterlauncher StartWecom + private TLS WeCom protocol peer + default shared MainPrint + official DeepSeek Write/Read/approval + actual Server and adapter cold restart same SID + close/join; no real WeCom account or message claim", "endpoint": "https://api.deepseek.com/anthropic", "model": "deepseek-flash[1m]", "catalog": "ordinary default, no forced Tools list or bare", "sessionId": sessionID, "fakeModelRequests": fakeRequests.Load(), "actualPermissionCallbacks": approvals.Load(), "actualPlatformConnections": connections.Load(), "actualReplyACKs": acks.Load(), "actualFileExact": true, "sameSIDColdServerAndAdapterRestart": true, "credentialInCanonical": false}
		trace, e := os.ReadFile(filepath.Join(config, "cc-haha", "traces", sessionID+".jsonl"))
		receipt["rawTracePersisted"] = e == nil && len(trace) > 0
		for name, value := range map[string][]byte{"receipt.json": raw(receipt), "artifact.txt": file, "canonical.jsonl": canonical} {
			if e := os.WriteFile(filepath.Join(receiptRoot, name), value, 0600); e != nil {
				t.Fatal(e)
			}
		}
		if e == nil && len(trace) > 0 {
			if e := os.WriteFile(filepath.Join(receiptRoot, "trace-private.jsonl"), trace, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
}
