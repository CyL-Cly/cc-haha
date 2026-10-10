package imruntime

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type feishuSourceRedirect struct {
	peer *url.URL
	base http.RoundTripper
}

func (r feishuSourceRedirect) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "open.feishu.cn" && req.URL.Host != "open.larksuite.com" {
		return nil, errors.New("unowned Feishu target")
	}
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = r.peer.Scheme
	u.Host = r.peer.Host
	copy.URL = &u
	return r.base.RoundTrip(copy)
}
func feishuSourceClient(t *testing.T, peer *httptest.Server, now func() time.Time, domain string) *FeishuClient {
	t.Helper()
	target, _ := url.Parse(peer.URL)
	c, e := NewFeishuClient(context.Background(), FeishuClientOptions{Credentials: feishuObject(feishuField("appId", "fixture-feishu"), feishuField("appSecret", "fixture-secret")), Domain: domain, HTTP: &http.Client{Transport: feishuSourceRedirect{target, peer.Client().Transport}}, Cache: NewFeishuTokenCache(), Now: now})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(c.Close)
	return c
}
func feishuSourceError(e error) map[string]any {
	var kit *FeishuCardKitError
	var httpErr *FeishuHTTPError
	if errors.As(e, &kit) {
		return map[string]any{"error": e.Error(), "code": kit.Code}
	}
	if errors.As(e, &httpErr) {
		return map[string]any{"error": fmt.Sprintf("HTTP:%d", httpErr.Status)}
	}
	return map[string]any{"error": e.Error()}
}
func TestActualFeishuSDKAndOriginalMediaCardKitHTTPAuthenticationCacheAndPackets(t *testing.T) {
	for _, domain := range []string{"feishu", "lark"} {
		t.Run(domain, func(t *testing.T) {
			now := int64(1770000000000)
			card := json.RawMessage(`{"schema":"2.0","body":{"elements":[{"tag":"markdown","content":"中文 🧡"}]}}`)
			actions := []map[string]any{{"method": "bot"}, {"method": "text", "text": "hello 🧡 <id>&"}, {"method": "text", "text": "reply text", "reply": "original-message"}, {"method": "card", "card": card}, {"method": "create", "card": card}, {"method": "send", "id": "card-fixture"}, {"method": "send", "id": "card-fixture", "reply": "original-message"}, {"method": "stream", "id": "card-fixture", "text": "accumulated 中文", "sequence": 2}, {"method": "settings", "id": "card-fixture", "streaming": false, "sequence": 3}, {"method": "update", "id": "card-fixture", "card": card, "sequence": 4}, {"method": "patch", "id": "message-fixture", "card": card}, {"method": "download", "id": "image-fixture", "kind": "image"}, {"method": "download", "id": "file-fixture", "kind": "file", "name": "report.DOCX"}, {"method": "image", "bytes": base64.StdEncoding.EncodeToString([]byte("private-image"))}, {"method": "file", "name": "REPORT.PDF", "bytes": base64.StdEncoding.EncodeToString([]byte("private-file"))}, {"method": "image", "bytes": base64.StdEncoding.EncodeToString([]byte("nested-image"))}, {"method": "file", "name": "nested.OPUS", "bytes": base64.StdEncoding.EncodeToString([]byte("nested-file"))}, {"method": "sendImage", "id": "uploaded-image"}, {"method": "sendFile", "id": "uploaded-file"}, {"method": "bot", "now": now + 341999}, {"method": "bot", "now": now + 342000}}
			run := func(source bool) (json.RawMessage, json.RawMessage) {
				var mu sync.Mutex
				packets := []any{}
				tokenCount := 0
				peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					packet := map[string]any{"method": req.Method, "path": req.URL.RequestURI(), "auth": req.Header.Get("Authorization"), "ua": req.Header.Get("User-Agent"), "accept": req.Header.Get("Accept")}
					var multipartBytes []byte
					if strings.HasPrefix(req.Header.Get("Content-Type"), "multipart/") {
						parts := []any{}
						reader, e := req.MultipartReader()
						if e != nil {
							t.Error(e)
							return
						}
						for {
							part, e := reader.NextPart()
							if e == io.EOF {
								break
							}
							if e != nil {
								t.Error(e)
								return
							}
							p, _ := io.ReadAll(part)
							multipartBytes = append(multipartBytes, p...)
							parts = append(parts, map[string]any{"name": part.FormName(), "filename": part.FileName(), "type": part.Header.Get("Content-Type"), "bytes": base64.StdEncoding.EncodeToString(p)})
						}
						packet["parts"] = parts
					} else {
						p, _ := io.ReadAll(req.Body)
						if len(p) > 0 {
							packet["body"] = json.RawMessage(p)
						}
					}
					mu.Lock()
					packets = append(packets, packet)
					mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					switch req.URL.Path {
					case "/open-apis/auth/v3/tenant_access_token/internal":
						tokenCount++
						_, _ = w.Write([]byte(fmt.Sprintf(`{"tenant_access_token":"private-tenant-%d","expire":522}`, tokenCount)))
					case "/open-apis/bot/v3/info":
						_, _ = w.Write([]byte(`{"code":0,"bot":{"open_id":"fixture-bot"}}`))
					case "/open-apis/cardkit/v1/cards":
						_, _ = w.Write([]byte(`{"code":0,"data":{"card_id":"card-fixture"}}`))
					case "/open-apis/im/v1/images":
						if strings.Contains(string(multipartBytes), "nested-image") {
							_, _ = w.Write([]byte(`{"code":0,"data":{"data":{"image_key":"nested-image-key"}}}`))
						} else {
							_, _ = w.Write([]byte(`{"code":0,"data":{"image_key":"usual-image-key"}}`))
						}
					case "/open-apis/im/v1/files":
						if strings.Contains(string(multipartBytes), "nested-file") {
							_, _ = w.Write([]byte(`{"code":0,"data":{"data":{"file_key":"nested-file-key"}}}`))
						} else {
							_, _ = w.Write([]byte(`{"code":0,"data":{"file_key":"usual-file-key"}}`))
						}
					default:
						if strings.Contains(req.URL.Path, "/resources/") {
							_, _ = w.Write([]byte("private-resource-bytes"))
						} else {
							_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"message-fixture"}}`))
						}
					}
				}))
				defer peer.Close()
				home := t.TempDir()
				var result json.RawMessage
				if source {
					result = runPlatformStageSource(t, "native-feishu-transport.fixture.ts", "CC_HAHA_FEISHU_TRANSPORT_FIXTURE", map[string]any{"mode": "api", "peer": peer.URL, "now": now, "domain": domain, "actions": actions}, home)
				} else {
					clock := now
					c := feishuSourceClient(t, peer, func() time.Time { return time.UnixMilli(clock) }, domain)
					store := NewAttachmentStore(home)
					media := NewFeishuMedia(c, store)
					rows := []any{}
					for _, a := range actions {
						if n, ok := a["now"].(int64); ok {
							clock = n
						}
						id, _ := a["id"].(string)
						reply, _ := a["reply"].(string)
						text, _ := a["text"].(string)
						kind, _ := a["kind"].(string)
						name, _ := a["name"].(string)
						sequence, _ := a["sequence"].(int)
						p, _ := a["card"].(json.RawMessage)
						var value json.RawMessage
						var e error
						switch a["method"] {
						case "bot":
							value, e = c.BotInfo(context.Background())
						case "text":
							value, e = c.SendText(context.Background(), NewText("chat-fixture"), NewText(text), NewText(reply))
						case "card":
							value, e = c.SendCard(context.Background(), NewText("chat-fixture"), p)
						case "create":
							value, e = c.CreateCardEntity(context.Background(), p)
						case "send":
							value, e = c.SendCardAsMessage(context.Background(), NewText("chat-fixture"), raw(id), NewText(reply), raw("fixture-uuid"))
						case "stream":
							e = c.StreamCardContent(context.Background(), NewText(id), NewText("streaming_content"), NewText(text), int64(sequence))
						case "settings":
							stream, _ := a["streaming"].(bool)
							e = c.SetCardStreamingMode(context.Background(), NewText(id), stream, int64(sequence))
						case "update":
							e = c.UpdateCardKitCard(context.Background(), NewText(id), p, int64(sequence))
						case "patch":
							value, e = c.PatchMessage(context.Background(), NewText(id), p)
						case "download":
							var local LocalAttachment
							local, e = media.DownloadResource(context.Background(), FeishuDownloadParams{MessageID: NewText("message-fixture"), FileKey: NewText(id), Kind: kind, FileName: NewText(name), SessionID: "session-fixture"})
							if e == nil {
								saved, err := os.ReadFile(local.Path)
								if err != nil {
									t.Fatal(err)
								}
								value = raw(map[string]any{"kind": local.Kind, "name": local.Name, "size": local.Size, "mimeType": local.MIME, "bytes": base64.StdEncoding.EncodeToString(local.Buffer), "saved": base64.StdEncoding.EncodeToString(saved)})
							}
						case "image":
							p, _ := base64.StdEncoding.DecodeString(a["bytes"].(string))
							value, e = media.UploadImage(context.Background(), p, "image/png")
						case "file":
							p, _ := base64.StdEncoding.DecodeString(a["bytes"].(string))
							value, e = media.UploadFile(context.Background(), p, name)
						case "sendImage":
							e = media.SendImageMessage(context.Background(), NewText("chat-fixture"), raw(id))
						case "sendFile":
							e = media.SendFileMessage(context.Background(), NewText("chat-fixture"), raw(id))
						}
						if e != nil {
							rows = append(rows, feishuSourceError(e))
						} else {
							if value == nil {
								value = raw(nil)
							}
							rows = append(rows, map[string]any{"value": value})
						}
					}
					c.Close()
					result = raw(map[string]any{"rows": rows, "origin": c.Origin})
				}
				mu.Lock()
				receipt := raw(packets)
				mu.Unlock()
				return result, receipt
			}
			want, wantPackets := run(true)
			got, gotPackets := run(false)
			exactJSON(t, got, want, "actual SDK + original Feishu producer return values")
			exactJSON(t, gotPackets, wantPackets, "actual SDK + original Feishu HTTP packets")
		})
	}
}
func TestActualFeishuSourceExtractPayloadRawUTF16FlatNullAndMediaTypes(t *testing.T) {
	values := []string{`{"text":"raw-\ud800-中文"}`, `{"text":7}`, `{"image_key":"image"}`, `{"image_key":""}`, `{"file_key":"file","file_name":"报告.PDF"}`, `{"file_key":7}`, `{"zh_cn":{"content":[[{"tag":"text","text":"A"},{"tag":"md","content":"B"},{"tag":"img","image_key":""},{"tag":"file","file_key":"f","file_name":"name"}]]}}`, `{"zh_cn":{"content":false},"en_us":{"content":[]}}`, `{"zh_cn":{"content":null},"en_us":{"content":[[{"tag":"md","text":"EN"}]]}}`, `null`, `false`, `"literal"`, `not-json`}
	rows := []map[string]any{}
	for _, kind := range []string{"text", "image", "file", "file_archive", "post", "other"} {
		for _, content := range values {
			rows = append(rows, map[string]any{"type": kind, "content": content})
		}
	}
	want := runPlatformStageSource(t, "native-feishu-transport.fixture.ts", "CC_HAHA_FEISHU_TRANSPORT_FIXTURE", map[string]any{"mode": "extract", "rows": rows}, t.TempDir())
	got := []any{}
	for _, row := range rows {
		value, e := FeishuExtractInbound(NewText(row["content"].(string)), row["type"].(string))
		if e != nil {
			category := "other"
			if strings.Contains(e.Error(), "null") {
				category = "null"
			} else if strings.Contains(e.Error(), "flat") {
				category = "flat"
			}
			got = append(got, map[string]any{"error": category})
		} else {
			got = append(got, map[string]any{"value": value})
		}
	}
	exactJSON(t, raw(got), want, "actual Source Feishu inbound content extraction")
}
func TestActualFeishuSDKEncryptedEventFlatteningSignatureAndFrameCodec(t *testing.T) {
	body := json.RawMessage(`{"schema":"2.0","header":{"event_type":"im.message.receive_v1","event_id":"event-1"},"event":{"message":{"chat_id":"chat-1","content":"fixture"}}}`)
	key := NewText("fixture-encrypt-key")
	aesKey := sha256.Sum256([]byte(key.String()))
	block, _ := aes.NewCipher(aesKey[:])
	pad := 16 - len(body)%16
	plain := append(append([]byte{}, body...), bytesRepeat(byte(pad), pad)...)
	iv := make([]byte, 16)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(plain, plain)
	encrypted := base64.StdEncoding.EncodeToString(append(iv, plain...))
	values := []map[string]any{{"raw": string(body), "check": false}, {"raw": string(raw(map[string]any{"encrypt": encrypted, "extra": "outer"})), "check": false}, {"raw": `{"encrypt":"bad"}`, "check": false}, {"raw": `{"schema":"2.0","header":{"event_type":"card.action.trigger"},"event":{"action":{"value":{"action":"permit"}}}}`, "check": false}, {"raw": `{"event":{"type":"unknown"},"token":"unchecked-token"}`, "check": false}, {"raw": string(body), "check": true}, {"raw": `{"schema":"2.0","header":{"event_type":"im.message.receive_v1"},"headers":{"x-lark-request-timestamp":"1","x-lark-request-nonce":"2","x-lark-signature":"bad"},"event":{}}`, "check": true}}
	for _, headers := range []protocol.JSONObject{feishuObject(feishuField("x-lark-request-timestamp", "1"), feishuField("x-lark-request-nonce", "2")), feishuObject(feishuField("x-lark-request-timestamp", 1), feishuField("x-lark-request-nonce", 2))} {
		signature, e := FeishuEventSignatureWithHeaders(body, headers, key)
		if e != nil {
			t.Fatal(e)
		}
		headers.Set("x-lark-signature", raw(signature))
		values = append(values, map[string]any{"raw": string(body), "check": true, "headers": headers})
	}
	want := runPlatformStageSource(t, "native-feishu-transport.fixture.ts", "CC_HAHA_FEISHU_TRANSPORT_FIXTURE", map[string]any{"mode": "event", "rows": values, "encryptKey": key.String()}, t.TempDir())
	d := NewFeishuDispatcher(key, NewText("fixture-verify"))
	events := []any{}
	for _, name := range []string{"im.message.receive_v1", "card.action.trigger", "fixture.fail"} {
		name := name
		d.Register(name, func(ctx context.Context, b protocol.JSONObject) (json.RawMessage, error) {
			events = append(events, map[string]any{"name": name, "body": b})
			if name == "card.action.trigger" {
				return json.RawMessage(`{"toast":{"type":"info","content":"✅ 已允许"}}`), nil
			}
			return nil, nil
		})
	}
	gotRows := []any{}
	for _, row := range values {
		var value json.RawMessage
		var e error
		if headers, ok := row["headers"].(protocol.JSONObject); ok {
			value, e = d.InvokeHTTP(context.Background(), json.RawMessage(row["raw"].(string)), headers)
		} else {
			value, e = d.Invoke(context.Background(), json.RawMessage(row["raw"].(string)), row["check"].(bool))
		}
		if e != nil {
			gotRows = append(gotRows, map[string]any{"error": true})
		} else {
			if value == nil {
				value = raw(nil)
			}
			gotRows = append(gotRows, map[string]any{"value": value})
		}
	}
	exactJSON(t, raw(map[string]any{"rows": gotRows, "events": events}), want, "actual SDK EventDispatcher parse/AES/signature boundary")
}
func bytesRepeat(v byte, n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = v
	}
	return p
}

func feishuFrameJSON(f FeishuFrame) map[string]any {
	v := map[string]any{"SeqID": f.SeqID, "LogID": f.LogID, "service": f.Service, "method": f.Method, "headers": f.Headers}
	if f.Payload != nil {
		v["payload"] = base64.StdEncoding.EncodeToString(f.Payload)
	}
	if f.PayloadEncoding != nil {
		v["payloadEncoding"] = *f.PayloadEncoding
	}
	if f.PayloadType != nil {
		v["payloadType"] = *f.PayloadType
	}
	if f.LogIDNew != nil {
		v["LogIDNew"] = *f.LogIDNew
	}
	return v
}

type feishuSourceCapture struct {
	mu     sync.Mutex
	frames []any
}

func (s *feishuSourceCapture) Read() ([]byte, error) { return nil, io.EOF }
func (s *feishuSourceCapture) SendBinary(p []byte) error {
	f, e := DecodeFeishuFrame(p)
	if e != nil {
		return e
	}
	s.mu.Lock()
	s.frames = append(s.frames, feishuFrameJSON(f))
	s.mu.Unlock()
	return nil
}
func (s *feishuSourceCapture) Close(string) error { return nil }
func TestActualFeishuSDKBinaryFrameCodecRequiredFieldsAndUnknownWire(t *testing.T) {
	str := func(s string) *string { return &s }
	frames := []FeishuFrame{{Headers: []FeishuHeader{}}, {SeqID: 128, LogID: 2000, Service: 7, Method: 1, Headers: []FeishuHeader{{"type", "event"}, {"seq", "0"}}, Payload: []byte("中文"), PayloadEncoding: str("utf8"), PayloadType: str("application/json"), LogIDNew: str("log-new")}, {SeqID: 1 << 33, LogID: 1 << 34, Service: -1, Method: 1, Headers: []FeishuHeader{{"key", "value"}}, Payload: []byte{}}}
	frames = append(frames, FeishuFrame{SeqID: ^uint64(0), LogID: ^uint64(0), Headers: []FeishuHeader{}})
	rows := []map[string]any{}
	got := []any{}
	for _, f := range frames {
		p := EncodeFeishuFrame(f)
		rows = append(rows, map[string]any{"frame": feishuFrameJSON(f)})
		decoded, e := DecodeFeishuFrame(p)
		if e != nil {
			t.Fatal(e)
		}
		got = append(got, map[string]any{"bytes": base64.StdEncoding.EncodeToString(p), "decoded": feishuFrameJSON(decoded)})
	}
	rawFrames := [][]byte{{}, {8, 0}, {8, 0, 16, 0, 24, 0}, {8, 0, 16, 0, 24, 0, 32, 0, 42, 0}, append(EncodeFeishuFrame(frames[1]), []byte{80, 7}...), append(EncodeFeishuFrame(frames[0]), []byte{98, 3, 1}...)}
	rawFrames = append(rawFrames, []byte{8, 255, 255, 255, 255, 255, 255, 255, 255, 255, 1, 16, 0, 24, 0, 32, 0})
	for _, p := range rawFrames {
		rows = append(rows, map[string]any{"bytes": base64.StdEncoding.EncodeToString(p)})
		decoded, e := DecodeFeishuFrame(p)
		if e != nil {
			got = append(got, map[string]any{"error": true})
		} else {
			got = append(got, map[string]any{"bytes": base64.StdEncoding.EncodeToString(p), "decoded": feishuFrameJSON(decoded)})
		}
	}
	want := runPlatformStageSource(t, "native-feishu-transport.fixture.ts", "CC_HAHA_FEISHU_TRANSPORT_FIXTURE", map[string]any{"mode": "codec", "rows": rows}, t.TempDir())
	exactJSON(t, raw(got), want, "actual installed SDK protobuf encode/decode")
}
func TestActualFeishuSDKPongFragmentAssemblyNegativeRuntimeAndCallbackACK(t *testing.T) {
	event := []byte(`{"schema":"2.0","header":{"event_type":"im.message.receive_v1","event_id":"id"},"event":{"message":{"content":"中文 🧡","chat_id":"c"}}}`)
	frame := func(id string, sum, seq int, p []byte) FeishuFrame {
		return FeishuFrame{SeqID: uint64(seq + 5), LogID: 1234, Service: 9, Method: 1, Headers: []FeishuHeader{{"type", "event"}, {"message_id", id}, {"sum", fmt.Sprint(sum)}, {"seq", fmt.Sprint(seq)}, {"trace_id", "trace"}}, Payload: p}
	}
	rows := []FeishuFrame{{Headers: []FeishuHeader{{"type", "pong"}}, Payload: []byte(`{"PingInterval":21,"ReconnectCount":3,"ReconnectInterval":7,"ReconnectNonce":0.25}`)}, frame("fragments", 3, 2, event[101:]), frame("fragments", 3, 0, event[:47]), frame("fragments", 3, 1, event[47:101]), frame("card", 1, 0, []byte(`{"schema":"2.0","header":{"event_type":"card.action.trigger"},"event":{"action":{"value":{"action":"permit"}}}}`)), frame("fail", 1, 0, []byte(`{"event":{"type":"fixture.fail"}}`)), frame("unknown", 1, 0, []byte(`{"event":{"type":"unregistered"}}`)), {Method: 1, Headers: []FeishuHeader{{"type", "card"}}, Payload: []byte(`{}`)}, frame("invalid", 1, 0, []byte(`bad-json`)), frame("invalid", 2, 0, event)}
	for _, payload := range []string{"null", "false", "0", `""`, "true"} {
		rows = append(rows, frame("falsy-"+payload, 1, 0, []byte(payload)))
	}
	for _, sum := range []string{"", "0x1", " 1 "} {
		f := frame("coerced-"+sum, 1, 0, event)
		f.Headers[2].Value = sum
		f.Headers[3].Value = " 0 "
		rows = append(rows, f)
	}
	inputRows := []any{}
	for _, f := range rows {
		inputRows = append(inputRows, feishuFrameJSON(f))
	}
	want := runPlatformStageSource(t, "native-feishu-transport.fixture.ts", "CC_HAHA_FEISHU_TRANSPORT_FIXTURE", map[string]any{"mode": "frames", "rows": inputRows, "now": int64(1770000000000), "elapsed": 7}, t.TempDir())
	clock := int64(1770000000000)
	d := NewFeishuDispatcher(NewText("ignored-signature-in-WS"), nil)
	events := []any{}
	for _, name := range []string{"im.message.receive_v1", "card.action.trigger", "fixture.fail"} {
		name := name
		d.Register(name, func(ctx context.Context, b protocol.JSONObject) (json.RawMessage, error) {
			events = append(events, map[string]any{"name": name, "body": b})
			clock += 7
			if name == "fixture.fail" {
				return nil, errors.New("fixture callback failed")
			}
			if name == "card.action.trigger" {
				return json.RawMessage(`{"toast":{"type":"info","content":"✅ 已允许"}}`), nil
			}
			return nil, nil
		})
	}
	c, e := NewFeishuClient(context.Background(), FeishuClientOptions{Credentials: protocol.NewJSONObject()})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	ws, e := NewFeishuWS(context.Background(), FeishuWSOptions{Client: c, Dispatcher: d, Now: func() time.Time { return time.UnixMilli(clock) }})
	if e != nil {
		t.Fatal(e)
	}
	defer ws.Close()
	sink := &feishuSourceCapture{frames: []any{}}
	ws.socket = sink
	gotRows := []any{}
	for _, f := range rows {
		e := ws.HandleFrame(context.Background(), f)
		gotRows = append(gotRows, map[string]any{"error": e != nil})
	}
	cfg := ws.Config()
	got := raw(map[string]any{"rows": gotRows, "events": events, "frames": sink.frames, "config": map[string]any{"pingInterval": cfg.PingInterval, "reconnectCount": cfg.ReconnectCount, "reconnectInterval": cfg.ReconnectInterval, "reconnectNonce": cfg.ReconnectNonce}})
	exactJSON(t, got, want, "actual SDK fragment cache/ACK/pong/callback")
}
func TestActualFeishuSDKStartEndpointBinaryHTTPWSAndGracefulClose(t *testing.T) {
	frame := func(kind, id string, p []byte) FeishuFrame {
		return FeishuFrame{SeqID: 10, LogID: 20, Service: 9, Method: 1, Headers: []FeishuHeader{{"type", "event"}, {"message_id", id}, {"sum", "1"}, {"seq", "0"}}, Payload: p}
	}
	requests := []FeishuFrame{frame("event", "message", []byte(`{"schema":"2.0","header":{"event_type":"im.message.receive_v1"},"event":{"message":{"chat_id":"chat-private","message_id":"m","content":"private incoming"}}}`)), frame("event", "permission", []byte(`{"schema":"2.0","header":{"event_type":"card.action.trigger"},"event":{"action":{"value":{"action":"permit","allowed":true,"requestId":"approve"}},"context":{"open_chat_id":"chat-private"}}}`))}
	run := func(source bool) (json.RawMessage, json.RawMessage) {
		var mu sync.Mutex
		wire := []any{}
		endpoint := []any{}
		completed := make(chan struct{})
		var completeOnce sync.Once
		var peerURL string
		server := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
			defer conn.Close()
			var p []byte
			if e := websocket.Message.Receive(conn, &p); e != nil {
				return
			}
			ping, e := DecodeFeishuFrame(p)
			if e != nil {
				t.Error(e)
				return
			}
			mu.Lock()
			wire = append(wire, feishuFrameJSON(ping))
			mu.Unlock()
			for _, f := range requests {
				conn.PayloadType = websocket.BinaryFrame
				if e := websocket.Message.Send(conn, EncodeFeishuFrame(f)); e != nil {
					return
				}
				if e := websocket.Message.Receive(conn, &p); e != nil {
					return
				}
				ack, e := DecodeFeishuFrame(p)
				if e != nil {
					t.Error(e)
					return
				}
				mu.Lock()
				wire = append(wire, feishuFrameJSON(ack))
				mu.Unlock()
			}
			completeOnce.Do(func() { close(completed) })
			for {
				if websocket.Message.Receive(conn, &p) != nil {
					return
				}
			}
		}}
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			switch req.URL.Path {
			case "/socket":
				server.ServeHTTP(w, req)
			case "/callback/ws/endpoint":
				p, _ := io.ReadAll(req.Body)
				mu.Lock()
				endpoint = append(endpoint, map[string]any{"method": req.Method, "body": json.RawMessage(p), "locale": req.Header.Get("locale"), "ua": req.Header.Get("User-Agent"), "auth": req.Header.Get("Authorization")})
				mu.Unlock()
				_, _ = w.Write(raw(map[string]any{"code": 0, "data": map[string]any{"URL": "ws" + strings.TrimPrefix(peerURL, "http") + "/socket?device_id=private&service_id=9", "ClientConfig": map[string]any{"PingInterval": 1000, "ReconnectInterval": 1, "ReconnectNonce": 0, "ReconnectCount": 0}}}))
			case "/control/done":
				select {
				case <-completed:
					_, _ = w.Write([]byte(`{}`))
				case <-req.Context().Done():
				}
			default:
				t.Errorf("unexpected Feishu WS path %s", req.URL.Path)
			}
		}))
		peerURL = peer.URL
		defer peer.Close()
		var result json.RawMessage
		if source {
			result = runPlatformStageSource(t, "native-feishu-transport.fixture.ts", "CC_HAHA_FEISHU_TRANSPORT_FIXTURE", map[string]any{"mode": "ws", "peer": peer.URL, "now": int64(1770000000000)}, t.TempDir())
		} else {
			c := feishuSourceClient(t, peer, func() time.Time { return time.UnixMilli(1770000000000) }, "feishu")
			d := NewFeishuDispatcher(nil, nil)
			events := []any{}
			var eventMu sync.Mutex
			for _, name := range []string{"im.message.receive_v1", "card.action.trigger"} {
				name := name
				d.Register(name, func(ctx context.Context, b protocol.JSONObject) (json.RawMessage, error) {
					eventMu.Lock()
					events = append(events, map[string]any{"name": name, "body": b})
					eventMu.Unlock()
					if name == "card.action.trigger" {
						return json.RawMessage(`{"toast":{"type":"info","content":"✅ 已允许"}}`), nil
					}
					return nil, nil
				})
			}
			no := false
			ws, e := NewFeishuWS(context.Background(), FeishuWSOptions{Client: c, Dispatcher: d, AutoReconnect: &no, Now: func() time.Time { return time.UnixMilli(1770000000000) }})
			if e != nil {
				t.Fatal(e)
			}
			if e = ws.Start(); e != nil {
				t.Fatal(e)
			}
			select {
			case <-completed:
			case <-time.After(5 * time.Second):
				ws.Close()
				t.Fatal("actual binary Feishu socket did not receive callback ACKs")
			}
			ws.Close()
			c.Close()
			eventMu.Lock()
			result = raw(map[string]any{"events": events})
			eventMu.Unlock()
		}
		mu.Lock()
		receipt := raw(map[string]any{"endpoint": endpoint, "frames": wire})
		mu.Unlock()
		return result, receipt
	}
	want, wantWire := run(true)
	got, gotWire := run(false)
	exactJSON(t, got, want, "actual SDK and native persistent event consumer")
	exactJSON(t, gotWire, wantWire, "actual SDK/native endpoint auth authority and binary ping/ACK")
}
