package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
	wa "github.com/NanmiCoder/cc-haha/backend/internal/whatsappcompat"
	"github.com/coder/websocket"
	"go.mau.fi/whatsmeow"
	wab "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.mau.fi/whatsmeow/util/keys"
)

type waFlowObject map[string]json.RawMessage

func waFlowJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func waFlowObj(v json.RawMessage) waFlowObject {
	var o waFlowObject
	_ = json.Unmarshal(v, &o)
	return o
}
func waFlowString(v json.RawMessage) string { var s string; _ = json.Unmarshal(v, &s); return s }
func waFlowBytes(v json.RawMessage) []byte {
	if s := waFlowString(v); s != "" {
		b, _ := base64.StdEncoding.DecodeString(s)
		return b
	}
	o := waFlowObj(v)
	b, _ := base64.StdEncoding.DecodeString(waFlowString(o["data"]))
	return b
}
func waFlowOriginal(t *testing.T, fixture string, input any) waFlowObject {
	t.Helper()
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sourcePath, err := filepath.Abs("../whatsappcompat/testdata/" + fixture)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", sourcePath)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(waFlowJSON(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal("actual original Signal peer", e, stderr.String())
	}
	return waFlowObj(out)
}

type waFlowRoute struct{ peer *httptest.Server }

func (r waFlowRoute) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "web.whatsapp.com" && req.URL.Host != "mmg.whatsapp.net" {
		return nil, fmt.Errorf("unowned WhatsApp HTTP target")
	}
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = strings.SplitN(r.peer.URL, ":", 2)[0]
	u.Host = r.peer.Listener.Addr().String()
	copy.URL = &u
	return r.peer.Client().Transport.RoundTrip(copy)
}

type waFlowMediaStatusRoute struct{ base waFlowRoute }

func (r waFlowMediaStatusRoute) RoundTrip(req *http.Request) (*http.Response, error) {
	response, e := r.base.RoundTrip(req)
	if e != nil {
		return response, e
	}
	if req.URL.Host == "mmg.whatsapp.net" && req.URL.Path == "/expired" {
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return nil, &imruntime.WhatsAppMediaFetchStatusError{Status: response.StatusCode, Cause: fmt.Errorf("owned fetch status rejection")}
	}
	return response, nil
}

func TestActualWhatsAppNoiseSignalSharedMainPrintWriteApprovalReadAndColdSID(t *testing.T) {
	actualWhatsAppMainprint(t, "")
}

func TestActualWhatsAppOfficialDeepSeekSharedMainPrintLiveModel(t *testing.T) {
	token := os.Getenv("CC_HAHA_WHATSAPP_LIVE_TOKEN")
	if token == "" {
		t.Skip("separate maintainer-authorized official model evidence")
	}
	if os.Getenv("CC_HAHA_WHATSAPP_LIVE_GATE") != "1" {
		t.Fatal("unowned live model gate")
	}
	actualWhatsAppMainprint(t, token)
}

func actualWhatsAppMainprint(t *testing.T, liveToken string) {
	actualWhatsAppMediaMainprint(t, liveToken, 0, false)
}

func TestActualWhatsAppMediaFetchStatus404And410TLSNoiseSignalSharedMainPrint(t *testing.T) {
	for _, status := range []int{404, 410} {
		t.Run(fmt.Sprint(status), func(t *testing.T) { actualWhatsAppMediaMainprint(t, "", status, true) })
	}
}

func TestActualWhatsAppMediaStockHTTP404And410TLSNoiseSignalSharedMainPrint(t *testing.T) {
	for _, status := range []int{404, 410} {
		t.Run(fmt.Sprint(status), func(t *testing.T) { actualWhatsAppMediaMainprint(t, "", status, false) })
	}
}

func actualWhatsAppMediaMainprint(t *testing.T, liveToken string, expiredStatus int, fetchStatus bool) {
	mediaMode := expiredStatus != 0
	mediaData := []byte{}
	if mediaMode {
		pixels := image.NewNRGBA(image.Rect(0, 0, 123, 83))
		for y := 0; y < 83; y++ {
			for x := 0; x < 123; x++ {
				pixels.SetNRGBA(x, y, color.NRGBA{uint8(x * 7), uint8(y * 3), uint8(x + y), 137})
			}
		}
		var encoded bytes.Buffer
		if e := png.Encode(&encoded, pixels); e != nil {
			t.Fatal(e)
		}
		mediaData = encoded.Bytes()
	}
	mediaKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	var encryptedMedia []byte
	var sourceMedia waFlowObject
	if mediaMode {
		encryptedMedia = waFlowBytes(waFlowOriginal(t, "node_media.fixture.ts", map[string]any{"op": "encrypt", "key": mediaKey, "type": "image", "data": base64.StdEncoding.EncodeToString(mediaData)})["data"])
		sourceMedia = waFlowObj(waFlowOriginal(t, "node_media.fixture.ts", map[string]any{"op": "prepare", "data": base64.StdEncoding.EncodeToString(mediaData), "mime": "image/png", "caption": "private outbound"})["message"])
	}
	uploaded := make(chan []byte, 4)
	var retries atomic.Int32
	outboundPath := ""
	outboundImageSeen := false
	var requests atomic.Int32
	target := ""
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		if bytes.Contains(body, []byte("PRIVATE_OFFLINE_MUST_NOT_REACH_HARNESS")) {
			t.Error("actual offline append was delivered to model request")
		}
		switch requests.Add(1) {
		case 1:
			if mediaMode {
				hasImage := bytes.Contains(body, []byte(`"type":"image"`))
				if fetchStatus && (!hasImage || !bytes.Contains(body, []byte(base64.StdEncoding.EncodeToString(mediaData)))) {
					t.Error("status-rejected WhatsApp media did not reach actual shared model consumer")
				}
				if !fetchStatus && hasImage {
					t.Error("stock non-OK HTTP media incorrectly reached model")
				}
			}
			response(w, "wa-write", "Write", map[string]string{"file_path": target, "content": "WA_SHARED_MAINPRINT_FILE\n"}, "")
		case 2:
			response(w, "wa-read", "Read", map[string]string{"file_path": target}, "")
		case 3:
			finish := "WA_SHARED_MAINPRINT_DONE"
			if mediaMode {
				finish += "\n![private outbound](" + outboundPath + ")"
			}
			response(w, "wa-finish", "", nil, finish)
		case 4:
			response(w, "wa-resume-read", "Read", map[string]string{"file_path": target}, "")
		case 5:
			response(w, "wa-resume-finish", "", nil, "WA_SHARED_MAINPRINT_RESUMED")
		default:
			t.Error("duplicate/extra model request")
			http.Error(w, "unexpected model request", 500)
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Arguments.PermissionMode = "default"
	o.Arguments.Tools = nil
	o.Environment["DISABLE_TELEMETRY"] = "1"
	o.Environment["CC_HAHA_LOCAL_ACCESS_TOKEN"] = "private-wa-server-token"
	target = filepath.Join(o.WorkDir, "wa-mainprint-file.txt")
	if mediaMode {
		outboundPath = filepath.Join(o.WorkDir, "whatsapp-outbound.png")
		if e := os.WriteFile(outboundPath, mediaData, 0600); e != nil {
			t.Fatal(e)
		}
	}
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	if liveToken != "" {
		o.Arguments.Model = "deepseek-flash[1m]"
		o.Arguments.MaxTurns = 8
		o.Arguments.Tools = nil
		o.Arguments.SystemPrompt = ""
		o.Environment["ENABLE_TOOL_SEARCH"] = "false"
		o.Environment["CC_HAHA_TRACE_API_CALLS"] = "1"
		delete(o.Environment, "ANTHROPIC_API_KEY")
		delete(o.Environment, "ANTHROPIC_BASE_URL")
		provider := map[string]any{"id": "wa-official-validation", "presetId": "deepseek", "name": "Private WhatsApp official model", "baseUrl": "https://api.deepseek.com/anthropic", "apiKey": liveToken, "authStrategy": "auth_token", "apiFormat": "anthropic", "supportsNestedToolResultMedia": false, "toolSearchEnabled": false, "models": map[string]string{"main": "deepseek-flash[1m]", "haiku": "deepseek-flash[1m]", "sonnet": "deepseek-flash[1m]", "opus": "deepseek-flash[1m]"}, "modelContextWindows": map[string]int{"deepseek-flash": 1000000}, "autoCompactWindow": 1000000}
		if e := os.MkdirAll(filepath.Join(config, "cc-haha"), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(config, "cc-haha", "providers.json"), waFlowJSON(map[string]any{"schemaVersion": 6, "activeId": "wa-official-validation", "providers": []any{provider}, "providerOrder": []string{"wa-official-validation"}}), 0600); e != nil {
			t.Fatal(e)
		}
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	serverOptions := ServerOptions{Port: listener.Addr().(*net.TCPAddr).Port, Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}}
	server, e := NewDesktopServer(serverOptions)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = server.Close(context.Background()) }()
	desktop := httptest.NewUnstartedServer(server)
	_ = desktop.Listener.Close()
	desktop.Listener = listener
	desktop.Start()
	defer func() { desktop.Close() }()
	authDir := filepath.Join(config, "whatsapp-auth", "default")
	if e = os.MkdirAll(authDir, 0700); e != nil {
		t.Fatal(e)
	}
	setup := waFlowOriginal(t, "node_peer.fixture.ts", map[string]any{"op": "setup"})
	sourceA := setup["a"]
	b := waFlowObj(setup["b"])
	creds := waFlowObj(b["creds"])
	creds["me"] = waFlowJSON(map[string]any{"id": "222:3@s.whatsapp.net", "lid": "99222:3@lid", "name": "private-linked-user"})
	if e = os.WriteFile(filepath.Join(authDir, "creds.json"), waFlowJSON(creds), 0600); e != nil {
		t.Fatal(e)
	}
	for name, v := range waFlowObj(waFlowObj(b["values"])["session"]) {
		if e = os.WriteFile(filepath.Join(authDir, "session-"+name+".json"), v, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e = os.WriteFile(filepath.Join(config, "adapters.json"), waFlowJSON(map[string]any{"future": map[string]bool{"preserve": true}, "whatsapp": map[string]any{"authDir": authDir, "futurePlatform": map[string]bool{"keep": true}}}), 0600); e != nil {
		t.Fatal(e)
	}
	patch := map[string]any{"defaultProjectDir": o.WorkDir, "allowedProjectRoots": []string{o.WorkDir}, "whatsapp": map[string]any{"allowedUsers": []string{"111@s.whatsapp.net"}}}
	request, _ := http.NewRequest("PUT", desktop.URL+"/api/adapters", bytes.NewReader(waFlowJSON(patch)))
	request.Header.Set("Authorization", "Bearer private-wa-server-token")
	request.Header.Set("Content-Type", "application/json")
	saved, err := desktop.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	savedBytes, err := io.ReadAll(saved.Body)
	saved.Body.Close()
	if err != nil || saved.StatusCode != 200 || !bytes.Contains(savedBytes, []byte(`"preserve":true`)) || !bytes.Contains(savedBytes, []byte(`"keep":true`)) {
		t.Fatal("actual desktop config preserve", saved.StatusCode, err)
	}
	deadline := 40 * time.Second
	if liveToken != "" {
		deadline = 240 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	root, intermediate, static := keys.NewKeyPair(), keys.NewKeyPair(), keys.NewKeyPair()
	oldPin := whatsmeow.WACertPubKey
	whatsmeow.WACertPubKey = *root.Pub
	defer func() { whatsmeow.WACertPubKey = oldPin }()
	wires := make(chan *privateNoiseWire, 2)
	outgoing := make(chan wab.Node, 64)
	peerErrors := make(chan error, 8)
	closed := make(chan struct{}, 2)
	peerHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		if mediaMode && r.Method == "POST" {
			if r.TLS == nil {
				t.Error("upload was not physical loopback TLS")
			}
			data, e := io.ReadAll(r.Body)
			if e != nil {
				t.Error(e)
			}
			uploaded <- data
			io.WriteString(w, `{"url":"https://mmg.whatsapp.net/uploaded","direct_path":"/uploaded"}`)
			return
		}
		if mediaMode && (r.URL.Path == "/expired" || r.URL.Path == "/fresh") {
			if r.TLS == nil {
				t.Error("download was not physical loopback TLS")
			}
			if r.URL.Path == "/expired" {
				http.Error(w, "expired", expiredStatus)
			} else {
				w.Write(encryptedMedia)
			}
			return
		}
		defer func() { closed <- struct{}{} }()
		conn, e := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if e != nil {
			peerErrors <- e
			return
		}
		defer conn.CloseNow()
		wire, p, e := acceptPrivateNoise(ctx, conn, root, intermediate, static)
		if e != nil {
			peerErrors <- e
			return
		}
		if p.GetUsername() != 222 || p.GetDevice() != 3 {
			peerErrors <- fmt.Errorf("old linked Noise payload differs")
			return
		}
		wires <- wire
		if e = wire.Send(wab.Node{Tag: "success", Attrs: wab.Attrs{"lid": types.JID{User: "99222", Device: 3, Server: types.HiddenUserServer}, "t": time.Now().Unix()}}); e != nil {
			peerErrors <- e
			return
		}
		for {
			node, e := wire.Read()
			if e != nil {
				return
			}
			switch node.Tag {
			case "iq":
				result := wab.Node{Tag: "iq", Attrs: wab.Attrs{"id": node.Attrs["id"], "type": "result", "from": types.ServerJID}}
				if node.GetChildByTag("count").Tag == "count" {
					result.Content = []wab.Node{{Tag: "count", Attrs: wab.Attrs{"value": "50"}}}
				}
				if node.GetChildByTag("media_conn").Tag == "media_conn" {
					result.Content = []wab.Node{{Tag: "media_conn", Attrs: wab.Attrs{"auth": "private", "ttl": "3600", "auth_ttl": "3600", "max_buckets": "1"}, Content: []wab.Node{{Tag: "host", Attrs: wab.Attrs{"hostname": "mmg.whatsapp.net"}}}}}
				}
				if usync := node.GetChildByTag("usync"); usync.Tag == "usync" {
					list := usync.GetChildByTag("list")
					users := []wab.Node{}
					for _, u := range list.GetChildren() {
						jid, _ := u.Attrs["jid"].(types.JID)
						device := 0
						if jid.User == "222" || jid.User == "99222" {
							device = 3
						}
						users = append(users, wab.Node{Tag: "user", Attrs: wab.Attrs{"jid": jid}, Content: []wab.Node{{Tag: "devices", Content: []wab.Node{{Tag: "device-list", Content: []wab.Node{{Tag: "device", Attrs: wab.Attrs{"id": device, "key-index": "1"}}}}}}}})
					}
					result.Content = []wab.Node{{Tag: "usync", Content: []wab.Node{{Tag: "list", Content: users}}}}
				}
				if e = wire.Send(result); e != nil {
					return
				}
			case "receipt":
				if node.Attrs["type"] == "server-error" {
					retries.Add(1)
					id := fmt.Sprint(node.Attrs["id"])
					cipher := node.GetChildByTag("encrypt", "enc_p")
					iv := node.GetChildByTag("encrypt", "enc_iv")
					decoded := waFlowOriginal(t, "node_media.fixture.ts", map[string]any{"op": "decodeReceipt", "id": id, "key": mediaKey, "ciphertext": base64.StdEncoding.EncodeToString(cipher.Content.([]byte)), "iv": base64.StdEncoding.EncodeToString(iv.Content.([]byte))})
					if waFlowString(decoded["stanzaId"]) != id {
						t.Error("original SDK did not decrypt actual retry receipt")
					}
					retry := waFlowOriginal(t, "node_media.fixture.ts", map[string]any{"op": "retryNotification", "key": mediaKey, "id": id, "path": "/fresh"})
					if e := wire.Send(wab.Node{Tag: "notification", Attrs: wab.Attrs{"id": id, "from": types.NewJID("222", types.DefaultUserServer), "type": "mediaretry", "t": time.Now().Unix()}, Content: []wab.Node{{Tag: "rmr", Attrs: wab.Attrs{"jid": types.NewJID("111", types.DefaultUserServer), "from_me": "false"}}, {Tag: "encrypt", Content: []wab.Node{{Tag: "enc_p", Content: waFlowBytes(retry["ciphertext"])}, {Tag: "enc_iv", Content: waFlowBytes(retry["iv"])}}}}}); e != nil {
						peerErrors <- e
						return
					}
				}
			case "message":
				outgoing <- *node
				if e = wire.Send(wab.Node{Tag: "ack", Attrs: wab.Attrs{"id": node.Attrs["id"], "class": "message", "from": node.Attrs["to"], "t": time.Now().Unix()}}); e != nil {
					return
				}
			}
		}
	})
	var peer *httptest.Server
	if mediaMode {
		peer = httptest.NewTLSServer(peerHandler)
	} else {
		peer = httptest.NewServer(peerHandler)
	}
	defer peer.Close()
	env := map[string]string{}
	for k, v := range o.Environment {
		env[k] = v
	}
	env["ADAPTER_SERVER_URL"] = "ws" + strings.TrimPrefix(desktop.URL, "http")
	offlineSeen := make(chan struct{}, 1)
	var imageThumbnail func([]byte) (wa.NodeImageThumbnail, error)
	if mediaMode {
		library, err := filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
		if err != nil {
			t.Fatal(err)
		}
		codec, err := imageprocessing.BundledCodec(library)
		if err != nil {
			t.Fatal(err)
		}
		imageThumbnail = func(data []byte) (wa.NodeImageThumbnail, error) { return imruntime.WhatsAppImageThumbnail(codec, data) }
	}
	var mediaTransport http.RoundTripper = waFlowRoute{peer}
	if fetchStatus {
		mediaTransport = waFlowMediaStatusRoute{waFlowRoute{peer}}
	}
	runtimeExits := map[*imruntime.WhatsAppRuntime]<-chan error{}
	newRuntime := func() *imruntime.WhatsAppRuntime {
		started, exited := make(chan *imruntime.WhatsAppRuntime, 1), make(chan error, 1)
		go func() {
			code, err := adapterlauncher.Run(ctx, []string{"--app-root", "/private/fixture/app.asar", "--whatsapp"}, adapterlauncher.Options{
				Environment: env, HomeDir: home, WorkDir: o.WorkDir, Stderr: io.Discard,
				StartWhatsApp: func(owned context.Context, options adapterlauncher.Options) (adapterlauncher.Process, error) {
					cfg := imruntime.LoadPlatformConfig("whatsapp", options.HomeDir, options.WorkDir, options.Environment, func(string) {})
					r, err := imruntime.NewWhatsAppRuntime(owned, imruntime.WhatsAppRuntimeOptions{Config: cfg, ServerHTTP: desktop.Client(), ClientOptions: wa.NodeWhatsAppOptions{HTTPTransport: mediaTransport, ImageThumbnail: imageThumbnail, OnEvent: func(value any) {
						if event, ok := value.(*events.Message); ok && event.Info.ID == "PRIVATE_WA_INPUT_1" {
							offlineSeen <- struct{}{}
						}
					}}})
					if err != nil {
						return nil, err
					}
					r.Start()
					started <- r
					return r, nil
				},
			})
			if err == nil && code != 0 {
				err = fmt.Errorf("public launcher code %d", code)
			}
			exited <- err
		}()
		select {
		case runtime := <-started:
			runtimeExits[runtime] = exited
			return runtime
		case err := <-exited:
			t.Fatal("actual WhatsApp public launcher", err)
		case <-ctx.Done():
			t.Fatal("actual WhatsApp public launcher startup", ctx.Err())
		}
		return nil
	}
	closeRuntime := func(runtime *imruntime.WhatsAppRuntime) {
		runtime.Close()
		select {
		case err := <-runtimeExits[runtime]:
			if err != nil {
				t.Fatal("public launcher close", err)
			}
		case <-ctx.Done():
			t.Fatal("public launcher did not join", ctx.Err())
		}
		delete(runtimeExits, runtime)
	}
	runtime := newRuntime()
	defer func() {
		if runtime != nil {
			closeRuntime(runtime)
		}
	}()
	var wire *privateNoiseWire
	select {
	case wire = <-wires:
	case e := <-peerErrors:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal("real Noise connection missing")
	}
	inbound := 0
	sendIncoming := func(text string, offline bool) {
		inbound++
		content := map[string]any{"conversation": text}
		if mediaMode && inbound == 2 {
			content = map[string]any{"imageMessage": map[string]any{"url": "https://mmg.whatsapp.net/expired", "directPath": "/expired", "mediaKey": mediaKey, "mimetype": "image/png", "caption": text}}
		}
		encrypted := waFlowOriginal(t, "node_message.fixture.ts", map[string]any{"op": "encrypt", "a": sourceA, "message": content})
		sourceA = encrypted["a"]
		packet := waFlowObj(encrypted["packet"])
		attrs := wab.Attrs{"id": fmt.Sprintf("PRIVATE_WA_INPUT_%d", inbound), "from": types.NewJID("111", types.DefaultUserServer), "type": "text", "t": time.Now().Unix(), "notify": "Private Peer"}
		if offline {
			attrs["offline"] = "1"
		}
		if e = wire.Send(wab.Node{Tag: "message", Attrs: attrs, Content: []wab.Node{{Tag: "enc", Attrs: wab.Attrs{"v": "2", "type": waFlowString(packet["type"])}, Content: waFlowBytes(packet["ciphertext"])}}}); e != nil {
			t.Fatal(e)
		}
	}
	send := func(text string) { sendIncoming(text, false) }

	approvalID := regexp.MustCompile(`需要权限确认 \[([^\]]+)\]`)
	approvalCount := 0
	consumeUntil := func(marker string) {
		for {
			select {
			case node := <-outgoing:
				participants := node.GetChildByTag("participants")
				var enc wab.Node
				for _, p := range participants.GetChildren() {
					jid, _ := p.Attrs["jid"].(types.JID)
					if jid.User == "111" {
						enc = p.GetChildByTag("enc")
					}
				}
				encrypted, ok := enc.Content.([]byte)
				if !ok {
					t.Fatal("actual encrypted participant reply absent")
				}
				result := waFlowOriginal(t, "node_message.fixture.ts", map[string]any{"op": "decrypt", "a": sourceA, "type": enc.Attrs["type"], "ciphertext": base64.StdEncoding.EncodeToString(encrypted)})
				sourceA = result["a"]
				content := waFlowObj(result["message"])
				if image := content["imageMessage"]; len(image) > 0 && mediaMode {
					m := waFlowObj(image)
					want := waFlowObj(sourceMedia["imageMessage"])
					if !bytes.Equal(m["jpegThumbnail"], want["jpegThumbnail"]) || !bytes.Equal(m["width"], want["width"]) || !bytes.Equal(m["height"], want["height"]) {
						t.Error("actual runtime thumbnail bytes or dimensions differ from Source", string(image), string(sourceMedia["imageMessage"]))
					}
					upload := <-uploaded
					path := filepath.Join(o.WorkDir, "physical-uploaded.cipher")
					if e := os.WriteFile(path, upload, 0600); e != nil {
						t.Fatal(e)
					}
					disk, _ := os.ReadFile(path)
					original := waFlowOriginal(t, "node_media.fixture.ts", map[string]any{"op": "decrypt", "data": base64.StdEncoding.EncodeToString(disk), "key": waFlowString(m["mediaKey"]), "type": "image"})
					if !bytes.Equal(waFlowBytes(original["data"]), mediaData) {
						t.Error("physical upload did not decrypt in original SDK")
					}
					outboundImageSeen = true
					if marker == "PRIVATE_MEDIA_SENT" {
						return
					}
					continue
				}
				text := waFlowString(content["conversation"])
				if match := approvalID.FindStringSubmatch(text); len(match) > 1 {
					approvalCount++
					send("/allow " + match[1])
				}
				if strings.TrimSpace(strings.SplitN(text, "\n", 2)[0]) == marker {
					return
				}
			case e := <-peerErrors:
				t.Fatal(e)
			case <-ctx.Done():
				t.Fatal("real encrypted shared Harness reply absent", marker, requests.Load(), approvalCount)
			}
		}
	}
	sendIncoming("PRIVATE_OFFLINE_MUST_NOT_REACH_HARNESS", true)
	select {
	case <-offlineSeen:
	case <-ctx.Done():
		t.Fatal("actual offline SDK event not delivered")
	}
	if runtime.Store.Get("111@s.whatsapp.net") != nil || requests.Load() != 0 {
		t.Fatal("offline append invoked current Harness", requests.Load())
	}
	prompt := "Use Write then Read on the requested private artifact, then finish."
	if liveToken != "" {
		prompt = fmt.Sprintf("Use Write to create %s with exact content WA_SHARED_MAINPRINT_FILE followed by a newline. Use Read to verify it, then respond only WA_SHARED_MAINPRINT_DONE. Do not use Bash or delegate.", target)
	}
	send(prompt)
	consumeUntil("WA_SHARED_MAINPRINT_DONE")
	if mediaMode {
		if !outboundImageSeen {
			consumeUntil("PRIVATE_MEDIA_SENT")
		}
		expected := int32(0)
		if fetchStatus {
			expected = 1
		}
		if retries.Load() != expected {
			t.Fatal("actual stock-vs-fetch-status retry count", retries.Load(), expected)
		}
		found := false
		filepath.WalkDir(runtime.Attachments.Root, func(path string, entry os.DirEntry, e error) error {
			if e == nil && !entry.IsDir() {
				data, _ := os.ReadFile(path)
				if bytes.Equal(data, mediaData) {
					found = true
				}
			}
			return nil
		})
		if found != fetchStatus {
			t.Fatal("actual attachment physical write differs from Source status branch", found, fetchStatus)
		}
	}
	data, e := os.ReadFile(target)
	if e != nil || string(data) != "WA_SHARED_MAINPRINT_FILE\n" || approvalCount < 1 || liveToken == "" && requests.Load() != 3 || liveToken != "" && requests.Load() != 0 {
		t.Fatal("actual shared MainPrint Write/Read/approval", e, approvalCount, requests.Load())
	}
	stored := runtime.Store.Get("111@s.whatsapp.net")
	if stored == nil {
		t.Fatal("real server binding absent")
	}
	sid := stored.SessionID
	closeRuntime(runtime)
	runtime = nil
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("first physical close absent")
	}
	// Restart the actual Go server as well as the transport client: restoration
	// must consume the canonical transcript, not a live in-memory session.
	if e = server.Close(ctx); e != nil {
		t.Fatal(e)
	}
	desktop.Close()
	freshListener, listenErr := net.Listen("tcp", listener.Addr().String())
	if listenErr != nil {
		t.Fatal(listenErr)
	}
	server, e = NewDesktopServer(serverOptions)
	if e != nil {
		freshListener.Close()
		t.Fatal(e)
	}
	desktop = httptest.NewUnstartedServer(server)
	_ = desktop.Listener.Close()
	desktop.Listener = freshListener
	desktop.Start()
	runtime = newRuntime()
	select {
	case wire = <-wires:
	case e := <-peerErrors:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal("cold Noise connection absent")
	}
	resumePrompt := "Read the existing artifact, then finish."
	if liveToken != "" {
		resumePrompt = fmt.Sprintf("Continue the same session. Use Read to verify %s still contains WA_SHARED_MAINPRINT_FILE, then respond only WA_SHARED_MAINPRINT_RESUMED. Do not use Bash or delegate.", target)
	}
	send(resumePrompt)
	consumeUntil("WA_SHARED_MAINPRINT_RESUMED")
	if cold := runtime.Store.Get("111@s.whatsapp.net"); cold == nil || cold.SessionID != sid || liveToken == "" && requests.Load() != 5 || liveToken != "" && requests.Load() != 0 {
		t.Fatal("cold WhatsApp changed canonical SID or Harness", requests.Load())
	}
	closeRuntime(runtime)
	runtime = nil
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("cold physical Close absent")
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer closeCancel()
	if e = server.Close(closeCtx); e != nil {
		t.Fatal(e)
	}
	if liveToken != "" {
		var canonical []byte
		filepath.WalkDir(filepath.Join(config, "projects"), func(path string, entry os.DirEntry, e error) error {
			if e == nil && !entry.IsDir() && entry.Name() == sid+".jsonl" {
				canonical, _ = os.ReadFile(path)
			}
			return nil
		})
		if bytes.Contains(canonical, []byte(liveToken)) {
			t.Fatal("credential in canonical")
		}
		for _, marker := range []string{`"name":"Write"`, `"name":"Read"`, "WA_SHARED_MAINPRINT_DONE", "WA_SHARED_MAINPRINT_RESUMED"} {
			if !bytes.Contains(canonical, []byte(marker)) {
				t.Fatal("live canonical missing", marker)
			}
		}
		root := os.Getenv("CC_HAHA_WHATSAPP_LIVE_RECEIPT_ROOT")
		if !filepath.IsAbs(root) {
			t.Fatal("private receipt root required")
		}
		if e = os.MkdirAll(root, 0700); e != nil {
			t.Fatal(e)
		}
		receipt := map[string]any{"status": "passed", "scope": "actual desktop PUT/ordinary public launcher, old Node credentials and sessions, private signed Noise and actual Node Signal encryption/decryption, WhatsAppRuntime -> shared Go MainPrint official DeepSeek default catalogue, encrypted IM approval, Write/Read actual file, cold adapter/server same canonical SID Read, physical peer/server Close; no real WhatsApp account/external messages claim", "sessionId": sid, "model": "deepseek-flash[1m]", "actualPermissionCallbacks": approvalCount, "fakeModelRequests": requests.Load(), "physicalExactFile": true, "sameSessionRestart": true, "coldServerRestart": true, "nativeNoisePeerConnections": 2}
		trace, err := os.ReadFile(filepath.Join(config, "cc-haha", "traces", sid+".jsonl"))
		if err != nil {
			t.Fatal("official trace capture", err)
		}
		if bytes.Contains(trace, []byte(liveToken)) {
			t.Fatal("credential in trace")
		}
		for name, value := range map[string][]byte{"receipt.json": waFlowJSON(receipt), "artifact.txt": data, "canonical.jsonl": canonical, "trace.jsonl": trace} {
			if e = os.WriteFile(filepath.Join(root, name), value, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	t.Log("actual original Node Signal, signed Noise old login, real Go default catalogue, encrypted approval reply, actual Write/Read physical artifact, cold adapter same SID, actual Close; all providers private fixtures")
}
