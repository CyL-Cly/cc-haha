package imruntime

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
)

func runWecomSource(t *testing.T, input any) protocol.JSONObject {
	return runWecomFixture(t, "wecom-leaves.fixture.ts", input)
}
func runWecomFixture(t *testing.T, fixtureName string, input any) protocol.JSONObject {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("private WeCom Source test requires explicitly pinned CC_HAHA_SOURCE_BUN")
	}
	fixture, err := filepath.Abs("../../testdata/" + fixtureName)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, "config"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_WECOM_PRIVATE_FIXTURE=1", "BUN_OPTIONS=--no-env-file"}
	if ca := object(raw(input)).String("ca"); ca != "" {
		path := filepath.Join(home, "private-peer-ca.pem")
		if err := os.WriteFile(path, []byte(ca), 0600); err != nil {
			t.Fatal(err)
		}
		cmd.Env = append(cmd.Env, "NODE_EXTRA_CA_CERTS="+path)
	}
	cmd.Stdin = bytes.NewReader(raw(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source: %v %s", err, stderr.String())
	}
	result := object(data)
	if !json.Valid(data) {
		t.Fatalf("invalid Source result %s %s", data, stderr.String())
	}
	return result
}
func wecomCaptured(value any, err error) protocol.JSONObject {
	if err != nil {
		return wecomJSONObject(wecomField("error", err.Error()))
	}
	return wecomJSONObject(wecomField("value", value))
}
func TestWecomActualSourcePayloadMediaUTF16BytesAndCrypto(t *testing.T) {
	payloads := []json.RawMessage{raw(nil), raw(map[string]any{}), json.RawMessage(`{"from":{"userid":" U\ud800 "},"msgid":" id\udfff ","text":{"content":" a\ud800 "},"voice":{"content":"voice"},"mixed":{"msg_item":[{"msgtype":"text","text":{"content":" mixed "}},{"msgtype":"image"}]},"quote":{"text":{"content":"q1\nq2"}}}`)}
	for _, body := range []string{`{"from":{"userid":"U"},"chattype":"group","text":{"content":"discard"}}`, `{"from":{"userid":4},"text":{"content":"discard"}}`, `{"from":{"userid":"U"},"chattype":"other","text":{"content":"中文😀中文😀中文😀中文😀中文😀中文😀中文😀中文😀中文😀"},"create_time":42}`, `{"from":{"userid":"U"},"text":{"content":false},"voice":{"content":"words"},"image":{"url":"https://private.test/a","aeskey":"key"},"file":{"url":"f"},"mixed":{"msg_item":[{"msgtype":"image","image":{"url":"m","aeskey":null}}]}}`, `{"msgid":17,"from":{"userid":"U"},"image":{"url":"url"}}`} {
		payloads = append(payloads, json.RawMessage(body))
	}
	chunks := []map[string]any{}
	for _, s := range []Text{NewText(""), NewText(strings.Repeat("中", 7000)), NewText(strings.Repeat("😀", 1500)), NewText("word one\n\n" + strings.Repeat("abc ", 2000)), Text{0xd800, 'x', 0xdc00}} {
		for _, n := range []int{0, 1, 3, 4, 4000, 19000} {
			chunks = append(chunks, map[string]any{"text": s, "limit": n})
		}
	}
	key := bytes.Repeat([]byte{7}, 32)
	cipherBlock, _ := aes.NewCipher(key)
	crypt := func(plain []byte) []byte {
		out := make([]byte, len(plain))
		cipher.NewCBCEncrypter(cipherBlock, key[:16]).CryptBlocks(out, plain)
		return out
	}
	decrypt := []map[string]any{}
	for _, size := range []int{0, 1, 16, 31, 32, 64} {
		plain := bytes.Repeat([]byte{9}, size)
		pad := 32 - size%32
		plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
		decrypt = append(decrypt, map[string]any{"data": base64.StdEncoding.EncodeToString(crypt(plain)), "key": base64.StdEncoding.EncodeToString(key)})
	}
	for _, pad := range []byte{0, 33} {
		plain := bytes.Repeat([]byte{pad}, 32)
		decrypt = append(decrypt, map[string]any{"data": base64.StdEncoding.EncodeToString(crypt(plain)), "key": base64.StdEncoding.EncodeToString(key)})
	}
	wrong := bytes.Repeat([]byte{2}, 32)
	wrong[30] = 3
	decrypt = append(decrypt, map[string]any{"data": base64.StdEncoding.EncodeToString(crypt(wrong)), "key": base64.StdEncoding.EncodeToString(key)})
	filenames := []string{"", `attachment; filename="a%20b.png"`, `attachment; filename*=UTF-8''%E4%B8%AD%E6%96%87.png; filename=wrong.txt`, `attachment; filename=a+b.txt`, `attachment; filename=%QQ.txt`, `attachment; filename=%FF.bin`}
	want := runWecomSource(t, map[string]any{"payloads": payloads, "chunks": chunks, "decrypt": decrypt, "filenames": filenames})
	presults, mresults := []protocol.JSONObject{}, []protocol.JSONObject{}
	for _, v := range payloads {
		p, e := ExtractWecomPayload(object(v))
		presults = append(presults, wecomCaptured(p, e))
		m, e := CollectWecomMediaCandidates(object(v), 1770000000123)
		mresults = append(mresults, wecomCaptured(m, e))
	}
	exactJSON(t, raw(presults), want.Get("payloads"), "payloads")
	exactJSON(t, raw(mresults), want.Get("media"), "media")
	chunkResults := [][]Text{}
	for _, v := range chunks {
		chunkResults = append(chunkResults, SplitWecomMessageByBytes(v["text"].(Text), v["limit"].(int)))
	}
	exactJSON(t, raw(chunkResults), want.Get("chunks"), "chunks")
	dresults := []protocol.JSONObject{}
	for _, v := range decrypt {
		data, _ := base64.StdEncoding.DecodeString(v["data"].(string))
		got, e := DecryptWecomFile(data, NewText(v["key"].(string)))
		dresults = append(dresults, wecomCaptured(base64.StdEncoding.EncodeToString(got), e))
	}
	exactJSON(t, raw(dresults), want.Get("decrypt"), "decrypt")
	fresults := []protocol.JSONObject{}
	for _, v := range filenames {
		got, e := WecomDownloadFilename(v)
		fresults = append(fresults, wecomCaptured(got, e))
	}
	exactJSON(t, raw(fresults), want.Get("filenames"), "filenames")
}

type wecomResponsePeer struct {
	calls         []protocol.JSONObject
	pending, fail bool
}

func (p *wecomResponsePeer) HasPendingReplyAck(protocol.JSONObject) bool { return p.pending }
func (p *wecomResponsePeer) ReplyStream(_ context.Context, frame protocol.JSONObject, id, text Text, finish bool) (protocol.JSONObject, error) {
	body := wecomJSONObject(wecomField("msgtype", "stream"), wecomField("stream", wecomJSONObject(wecomField("id", id), wecomField("finish", finish), wecomField("content", text))))
	p.calls = append(p.calls, wecomJSONObject(wecomField("cmd", "aibot_respond_msg"), wecomField("reqId", wecomReqID(frame)), wecomField("body", body)))
	if p.fail {
		return protocol.JSONObject{}, fmt.Errorf("private ack failure")
	}
	return protocol.JSONObject{}, nil
}
func (p *wecomResponsePeer) SendMessage(_ context.Context, chat Text, body protocol.JSONObject) (protocol.JSONObject, error) {
	full := wecomJSONObject(wecomField("chatid", chat))
	for _, f := range body.OwnProperties() {
		_ = full.SetPropertyJSON(f.NameJSON, f.Value)
	}
	p.calls = append(p.calls, wecomJSONObject(wecomField("cmd", "aibot_send_msg"), wecomField("body", full)))
	if p.fail {
		return protocol.JSONObject{}, fmt.Errorf("private ack failure")
	}
	return protocol.JSONObject{}, nil
}
func TestWecomActualSourceResponseAnchorsACKSkipOverflowAndFallback(t *testing.T) {
	frame := map[string]any{"headers": map[string]string{"req_id": "original"}}
	cases := []map[string]any{{"frame": frame, "actions": []any{map[string]any{"kind": "append", "text": "hello"}, map[string]any{"kind": "replace", "frame": map[string]any{"headers": map[string]string{"req_id": "new"}}}, map[string]any{"kind": "pending", "value": true}, map[string]any{"kind": "append", "text": " after"}, map[string]any{"kind": "pending", "value": false}, map[string]any{"kind": "finish"}, map[string]any{"kind": "finish"}}}, {"actions": []any{map[string]any{"kind": "append", "text": "orphan"}, map[string]any{"kind": "finish"}}}, {"frame": frame, "actions": []any{map[string]any{"kind": "append", "text": strings.Repeat("中", 6300)}, map[string]any{"kind": "append", "text": strings.Repeat("😀", 1200)}, map[string]any{"kind": "append", "text": " tail"}, map[string]any{"kind": "finish"}}}, {"frame": frame, "actions": []any{map[string]any{"kind": "fail", "value": true}, map[string]any{"kind": "append", "text": "failure"}, map[string]any{"kind": "finish"}}}, {"frame": frame, "actions": []any{map[string]any{"kind": "append", "text": "  "}, map[string]any{"kind": "finish"}}}}
	want := runWecomSource(t, map[string]any{"responses": cases})
	results := []any{}
	for _, v := range cases {
		p := &wecomResponsePeer{calls: []protocol.JSONObject{}}
		var f *protocol.JSONObject
		if v["frame"] != nil {
			x := object(raw(v["frame"]))
			f = &x
		}
		r := NewWecomResponse(context.Background(), p, NewText("U"), f, "12345678-1234-4321-9999-abcdefabcdef", nil)
		outcomes := []protocol.JSONObject{}
		for _, a := range v["actions"].([]any) {
			action := a.(map[string]any)
			switch action["kind"] {
			case "replace":
				if f != nil {
					*f = object(raw(action["frame"]))
				}
			case "pending":
				p.pending = action["value"].(bool)
			case "fail":
				p.fail = action["value"].(bool)
			default:
				var e error
				if action["kind"] == "append" {
					e = r.Append(action["text"].(string))
				} else {
					e = r.Finish()
				}
				out := wecomJSONObject()
				if e != nil {
					out.Set("error", raw(e.Error()))
				}
				outcomes = append(outcomes, out)
			}
		}
		results = append(results, map[string]any{"calls": p.calls, "outcomes": outcomes})
	}
	exactJSON(t, raw(results), want.Get("responses"), "responses")
}

func wecomPrivatePeer(t *testing.T, handler func(*websocket.Conn)) (*httptest.Server, func(context.Context, string, *tls.Config) (*Socket, error)) {
	t.Helper()
	server := httptest.NewTLSServer(websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: handler})
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	return server, func(ctx context.Context, address string, _ *tls.Config) (*Socket, error) {
		if address != WecomWebSocketURL {
			return nil, fmt.Errorf("production address changed: %s", address)
		}
		return DialSocket(ctx, "wss"+strings.TrimPrefix(server.URL, "https"), &tls.Config{RootCAs: roots})
	}
}
func wecomAck(conn *websocket.Conn, frame protocol.JSONObject, body any) error {
	response := wecomJSONObject(wecomField("headers", object(frame.Get("headers"))), wecomField("errcode", 0))
	if body != nil {
		response.Set("body", raw(body))
	}
	return websocket.Message.Send(conn, string(raw(response)))
}
func TestWecomActualSourceSDKUploadProtocolAndPhysicalTLSBytes(t *testing.T) {
	data := make([]byte, 512*1024+37)
	for i := range data {
		data[i] = byte(i % 251)
	}
	want := runWecomSource(t, map[string]any{"protocol": map[string]any{"bytes": base64.StdEncoding.EncodeToString(data)}})
	var mu sync.Mutex
	frames := []protocol.JSONObject{}
	parts := map[int][]byte{}
	errors := make(chan error, 1)
	peer, dial := wecomPrivatePeer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		for {
			var text string
			if e := websocket.Message.Receive(conn, &text); e != nil {
				return
			}
			f := object([]byte(text))
			cmd := f.String("cmd")
			if cmd == "aibot_subscribe" {
				if e := wecomAck(conn, f, nil); e != nil {
					errors <- e
					return
				}
				continue
			}
			record := wecomJSONObject(wecomField("cmd", cmd), protocol.JSONField{Name: "body", Value: f.Get("body")})
			mu.Lock()
			frames = append(frames, record)
			mu.Unlock()
			var body any
			switch cmd {
			case "aibot_upload_media_init":
				body = map[string]string{"upload_id": "upload-1"}
			case "aibot_upload_media_chunk":
				part := object(f.Get("body"))
				var index int
				_ = json.Unmarshal(part.Get("chunk_index"), &index)
				bytes, e := base64.StdEncoding.DecodeString(part.String("base64_data"))
				if e != nil {
					errors <- e
					return
				}
				mu.Lock()
				parts[index] = bytes
				mu.Unlock()
			case "aibot_upload_media_finish":
				body = map[string]string{"media_id": "media-1", "type": "image", "created_at": "2026-02-02T02:40:00.123Z"}
			}
			if e := wecomAck(conn, f, body); e != nil {
				errors <- e
				return
			}
		}
	})
	defer peer.Close()
	ready := make(chan struct{}, 1)
	client, e := NewWecomClient(context.Background(), WecomClientOptions{BotID: NewText("private-bot"), Secret: NewText("private-secret"), Dial: dial, OnAuthenticated: func() { ready <- struct{}{} }})
	if e != nil {
		t.Fatal(e)
	}
	client.Start()
	defer client.Close()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("auth not ready")
	}
	result, e := client.UploadMedia(context.Background(), data, NewText("image"), NewText("physical.png"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.SendMediaMessage(context.Background(), NewText("U"), NewText("image"), TextJSON(result.Get("media_id"))); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	actualFrames := append([]protocol.JSONObject{}, frames...)
	reassembled := append(append([]byte{}, parts[0]...), parts[1]...)
	mu.Unlock()
	if !bytes.Equal(reassembled, data) {
		t.Fatal("physical chunk bytes differ")
	}
	// The SDK dispatches concurrent chunk workers; their arrival order is not a
	// promised wire ordering. Match each actual index, retaining init/finish/send order.
	sortChunks := func(list []protocol.JSONObject) []protocol.JSONObject {
		copy := append([]protocol.JSONObject{}, list...)
		for i := 1; i < len(copy)-2; i++ {
			for j := i + 1; j < len(copy)-2; j++ {
				var a, b int
				_ = json.Unmarshal(object(copy[i].Get("body")).Get("chunk_index"), &a)
				_ = json.Unmarshal(object(copy[j].Get("body")).Get("chunk_index"), &b)
				if a > b {
					copy[i], copy[j] = copy[j], copy[i]
				}
			}
		}
		return copy
	}
	got := wecomJSONObject(wecomField("frames", sortChunks(actualFrames)), wecomField("result", result))
	wantProtocol := object(want.Get("protocol"))
	wantProtocol.Set("frames", raw(sortChunks(collectJSONArray(wantProtocol.Get("frames")))))
	exactJSON(t, raw(got), raw(wantProtocol), "protocol")
	select {
	case e := <-errors:
		t.Fatal(e)
	default:
	}
}

var _ = io.EOF
