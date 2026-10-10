package application

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	wa "github.com/NanmiCoder/cc-haha/backend/internal/whatsappcompat"
	"github.com/coder/websocket"
	"github.com/fsnotify/fsnotify"
	"go.mau.fi/libsignal/ecc"
	"go.mau.fi/whatsmeow"
	wab "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/proto/waWa6"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/util/keys"
	"google.golang.org/protobuf/proto"
)

type waRegistrationTransport struct{ peer *httptest.Server }

func (f waRegistrationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "web.whatsapp.com" || r.URL.Scheme != "https" {
		return nil, fmt.Errorf("unowned WhatsApp logical target")
	}
	copy := r.Clone(r.Context())
	u := *r.URL
	target, _ := url.Parse(f.peer.URL)
	u.Scheme, u.Host = target.Scheme, target.Host
	copy.URL = &u
	return f.peer.Client().Transport.RoundTrip(copy)
}
func waRegistrationFactory(t *testing.T, transport http.RoundTripper) (*server.Server, string) {
	t.Helper()
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	s, e := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, WhatsAppRegistrationHTTPTransport: transport, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s, o.Environment["CLAUDE_CONFIG_DIR"]
}
func waRegistrationPOST(t *testing.T, peer *httptest.Server, tail string, body string) (int, protocol.JSONObject) {
	t.Helper()
	res, e := peer.Client().Post(peer.URL+"/api/adapters/whatsapp/"+tail, "application/json", strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(res.Body)
	res.Body.Close()
	if e != nil {
		t.Fatal(e)
	}
	var value protocol.JSONObject
	if e = value.UnmarshalJSON(raw); e != nil {
		t.Fatalf("HTTP%d %v %s", res.StatusCode, e, raw)
	}
	return res.StatusCode, value
}
func TestDesktopWhatsAppActualNoiseQRPair515ConnectedOldCredentialsPromoteProjectionAndUnbind(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root, intermediate, static := keys.NewKeyPair(), keys.NewKeyPair(), keys.NewKeyPair()
	oldPin := whatsmeow.WACertPubKey
	whatsmeow.WACertPubKey = *root.Pub
	defer func() { whatsmeow.WACertPubKey = oldPin }()
	var connections atomic.Int32
	wires := make(chan *privateNoiseWire, 2)
	payloads := make(chan *waWa6.ClientPayload, 2)
	closed := make(chan struct{}, 2)
	errs := make(chan error, 2)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sw.js" {
			io.WriteString(w, `const client_revision=1021;`)
			return
		}
		if r.URL.Path != "/ws/chat" {
			t.Error("unexpected physical source path", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		conn, e := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if e != nil {
			errs <- e
			return
		}
		defer func() { conn.CloseNow(); closed <- struct{}{} }()
		wire, p, e := acceptPrivateNoise(ctx, conn, root, intermediate, static)
		if e != nil {
			errs <- e
			return
		}
		index := connections.Add(1)
		wires <- wire
		payloads <- p
		if index == 1 {
			e = wire.Send(wab.Node{Tag: "iq", Attrs: wab.Attrs{"id": "qr-source", "type": "set", "from": types.ServerJID}, Content: []wab.Node{{Tag: "pair-device", Content: []wab.Node{{Tag: "ref", Content: []byte("private-qr-reference")}}}}})
		} else {
			e = wire.Send(wab.Node{Tag: "success", Attrs: wab.Attrs{"lid": types.JID{User: "99222", Device: 3, Server: types.HiddenUserServer}, "t": time.Now().Unix()}})
		}
		if e != nil {
			errs <- e
			return
		}
		for {
			node, e := wire.Read()
			if e != nil {
				return
			}
			if node.Tag == "iq" {
				id := node.Attrs["id"]
				result := wab.Node{Tag: "iq", Attrs: wab.Attrs{"id": id, "type": "result", "from": types.ServerJID}}
				if node.GetChildByTag("count").Tag != "" {
					result.Content = []wab.Node{{Tag: "count", Attrs: wab.Attrs{"value": "50"}}}
				}
				if e = wire.Send(result); e != nil {
					return
				}
			}
		}
	}))
	defer upstream.Close()
	s, config := waRegistrationFactory(t, waRegistrationTransport{upstream})
	peer := httptest.NewServer(s)
	defer peer.Close()
	seed := map[string]any{"future": map[string]any{"preserve": true}, "telegram": map[string]any{"botToken": "private-other-token"}, "whatsapp": map[string]any{"futurePlatform": 17, "allowedUsers": []string{"retained-user"}, "pairedUsers": []any{map[string]any{"userId": "old", "pairedAt": 0}}}}
	expected := waOriginalBindingProjection(t, seed)
	if e := os.MkdirAll(config, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(config, "adapters.json"), waFlowJSON(seed), 0600); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(config, "whatsapp-auth", "default")
	if e := os.MkdirAll(target, 0700); e != nil {
		t.Fatal(e)
	}
	old := []byte(`{"old":"target-backup"}`)
	_ = os.WriteFile(filepath.Join(target, "old.json"), old, 0600)
	status, start := waRegistrationPOST(t, peer, "login/start", "")
	if status != 200 || !strings.HasPrefix(start.String("qr"), "private-qr-reference,") || !strings.HasPrefix(start.String("qrDataUrl"), "data:image/png;base64,") {
		t.Fatalf("actual SDK QR%d %s", status, waFlowJSON(start))
	}
	wire := <-wires
	p := <-payloads
	if p.GetDevicePairingData() == nil {
		t.Fatal("missing actual pairing payload")
	}
	parts := strings.Split(start.String("qr"), ",")
	identityPub, e := base64.StdEncoding.DecodeString(parts[2])
	if e != nil {
		t.Fatal(e)
	}
	adv, e := base64.StdEncoding.DecodeString(parts[3])
	if e != nil {
		t.Fatal(e)
	}
	phone := keys.NewKeyPair()
	details, _ := proto.Marshal(&waAdv.ADVDeviceIdentity{RawID: proto.Uint32(77), Timestamp: proto.Uint64(123456), KeyIndex: proto.Uint32(4)})
	sig := ecc.CalculateSignature(ecc.NewDjbECPrivateKey(*phone.Priv), append(append([]byte{6, 0}, details...), identityPub...))
	account, _ := proto.Marshal(&waAdv.ADVSignedDeviceIdentity{Details: details, AccountSignatureKey: phone.Pub[:], AccountSignature: sig[:]})
	mac := hmac.New(sha256.New, adv)
	mac.Write(account)
	container, _ := proto.Marshal(&waAdv.ADVSignedDeviceIdentityHMAC{Details: account, HMAC: mac.Sum(nil)})
	if e = wire.Send(wab.Node{Tag: "iq", Attrs: wab.Attrs{"id": "private-pair", "type": "set", "from": types.ServerJID, "t": time.Now().Unix()}, Content: []wab.Node{{Tag: "pair-success", Content: []wab.Node{{Tag: "device-identity", Content: container}, {Tag: "device", Attrs: wab.Attrs{"jid": types.NewADJID("222", 0, 3), "lid": types.JID{User: "99222", Server: types.HiddenUserServer, Device: 3}}}, {Tag: "biz", Attrs: wab.Attrs{"name": "Private Business"}}, {Tag: "platform", Attrs: wab.Attrs{"name": "android"}}}}}}); e != nil {
		t.Fatal(e)
	}
	// Observe the queued old Node credentials physically before requesting the
	// source-required515 reconnect; no timing sleep or synthetic Connected event.
	saved := make(chan struct{})
	watcher, e := fsnotify.NewWatcher()
	if e != nil {
		t.Fatal(e)
	}
	defer watcher.Close()
	rootDir := filepath.Join(config, "whatsapp-auth")
	entries, e := os.ReadDir(rootDir)
	if e != nil {
		t.Fatal(e)
	}
	staging := ""
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".login-") {
			staging = filepath.Join(rootDir, entry.Name())
		}
	}
	if staging == "" {
		t.Fatal("real staging absent")
	}
	if e = watcher.Add(staging); e != nil {
		t.Fatal(e)
	}
	check := func() bool {
		raw, e := os.ReadFile(filepath.Join(staging, "creds.json"))
		return e == nil && bytes.Contains(raw, []byte(`"222:3@s.whatsapp.net"`))
	}
	go func() {
		if check() {
			close(saved)
			return
		}
		for {
			select {
			case <-watcher.Events:
				if check() {
					close(saved)
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	select {
	case <-saved:
	case e := <-errs:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal("old credentials not actually saved")
	}
	if e = wire.Send(wab.Node{Tag: "stream:error", Attrs: wab.Attrs{"code": "515"}}); e != nil {
		t.Fatal(e)
	}
	select {
	case <-closed:
	case e := <-errs:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal("first socket did not close")
	}
	select {
	case <-wires:
	case e := <-errs:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal("actual restart socket missing")
	}
	p = <-payloads
	if p.GetDevicePairingData() != nil || p.GetUsername() != 222 || p.GetDevice() != 3 {
		t.Fatal("new socket not using saved old credentials")
	}
	// A physical status poll observes Connected; server/API performs promotion.
	var linked protocol.JSONObject
	for i := 0; i < 100; i++ {
		status, linked = waRegistrationPOST(t, peer, "login/poll", string(waFlowJSON(map[string]string{"sessionKey": start.String("sessionKey")})))
		if status != 200 {
			t.Fatal(status, waFlowJSON(linked))
		}
		if linked.Get("whatsapp") != nil {
			break
		}
		runtime.Gosched()
	}
	var masked map[string]any
	_ = json.Unmarshal(waFlowJSON(linked), &masked)
	section, _ := masked["whatsapp"].(map[string]any)
	if section == nil || section["accountJid"] != "222:3@s.whatsapp.net" || section["authDir"] != target || section["futurePlatform"] != float64(17) || len(section["pairedUsers"].([]any)) != 0 || len(section["allowedUsers"].([]any)) != 1 || masked["telegram"].(map[string]any)["botToken"] != "****oken" {
		t.Fatalf("actual binding projection %s", waFlowJSON(linked))
	}
	var originalLinked any
	sourceLinked := bytes.ReplaceAll(expected.rows[2], []byte(expected.home), []byte(filepath.Dir(config)))
	_ = json.Unmarshal(sourceLinked, &originalLinked)
	var currentLinked any
	_ = json.Unmarshal(waFlowJSON(linked), &currentLinked)
	if !reflect.DeepEqual(currentLinked, originalLinked) {
		t.Fatalf("actual Source binding projection Go%s Source%s", waFlowJSON(linked), sourceLinked)
	}
	device, _, e := wa.OpenNodeDevice(target, nil)
	if e != nil || device.ID == nil || device.ID.String() != "222:3@s.whatsapp.net" {
		t.Fatal("promoted old Node credentials not cold readable", e)
	}
	if _, e = os.Stat(staging); !os.IsNotExist(e) {
		t.Fatal("staging not physically promoted", e)
	}
	if _, e = os.Stat(filepath.Join(target, "old.json")); !os.IsNotExist(e) {
		t.Fatal("old target not replaced", e)
	}
	_, unbound := waRegistrationPOST(t, peer, "unbind", "")
	var sourceUnbound, actualUnbound any
	normalized := bytes.ReplaceAll(expected.rows[3], []byte(expected.home), []byte(filepath.Dir(config)))
	_ = json.Unmarshal(normalized, &sourceUnbound)
	_ = json.Unmarshal(waFlowJSON(unbound), &actualUnbound)
	if !reflect.DeepEqual(sourceUnbound, actualUnbound) {
		t.Fatalf("actual Source unbind projection Go%s Source%s", waFlowJSON(unbound), normalized)
	}
	if bytes.Contains(waFlowJSON(unbound), []byte(`"accountJid"`)) {
		t.Fatal("accountJid not deleted")
	}
	if _, e = os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("managed auth not physically unbound", e)
	}
	if e = s.Close(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("last actual socket escaped close")
	}
	if connections.Load() != 2 {
		t.Fatal("duplicate SDK/protocol autoreconnect", connections.Load())
	}
}

type waHeldRegistrationTransport struct {
	started  chan struct{}
	finished chan struct{}
	once     sync.Once
}

func (f *waHeldRegistrationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "web.whatsapp.com" {
		return nil, fmt.Errorf("unowned target")
	}
	f.once.Do(func() { close(f.started) })
	<-r.Context().Done()
	close(f.finished)
	return nil, r.Context().Err()
}
func TestDesktopWhatsAppServerCloseCancelsAcceptedBeginBeforeServiceCloseJoin(t *testing.T) {
	transport := &waHeldRegistrationTransport{started: make(chan struct{}), finished: make(chan struct{})}
	s, _ := waRegistrationFactory(t, transport)
	peer := httptest.NewServer(s)
	defer peer.Close()
	clientCtx, disconnect := context.WithCancel(context.Background())
	defer disconnect()
	request, _ := http.NewRequestWithContext(clientCtx, "POST", peer.URL+"/api/adapters/whatsapp/login/start", nil)
	client := make(chan error, 1)
	go func() {
		res, e := peer.Client().Do(request)
		if res != nil {
			res.Body.Close()
		}
		client <- e
	}()
	select {
	case <-transport.started:
	case <-time.After(5 * time.Second):
		t.Fatal("actual accepted Connect HTTP did not start")
	}
	disconnect()
	<-client
	select {
	case <-transport.finished:
		t.Fatal("renderer abort canceled server-owned login session")
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := s.Close(ctx); e != nil {
		t.Fatal("API join/registration Close dependency deadlock", e)
	}
	select {
	case <-transport.finished:
	default:
		t.Fatal("actual pending HTTP escaped close")
	}
}

type waOriginalProjection struct {
	home string
	rows []json.RawMessage
}

func waOriginalBindingProjection(t *testing.T, seed any) waOriginalProjection {
	t.Helper()
	home := t.TempDir()
	fixture, _ := filepath.Abs("../imregistration/testdata/whatsapp-api.fixture.ts")
	input, _ := json.Marshal(map[string]any{"home": home, "seed": seed, "actions": []any{map[string]any{"tail": "login/start"}, map[string]any{"tail": "login/poll", "raw": "$session"}, map[string]any{"tail": "login/poll", "raw": "$session", "event": "open"}, map[string]any{"tail": "unbind"}}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_REGISTRATION_FIXTURE=1", "NODE_ENV=test", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, e := cmd.Output()
	if e != nil {
		t.Fatal("original full API binding", e, stderr.String())
	}
	var source struct {
		Home string
		Rows []struct {
			Status int
			Body   json.RawMessage
		}
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(raw, &source); e != nil || len(source.Rows) != 4 || len(source.Capabilities.Blocked) != 0 {
		t.Fatalf("original API %v %s", e, raw)
	}
	result := waOriginalProjection{home: source.Home}
	for _, row := range source.Rows {
		if row.Status != 200 {
			t.Fatalf("actual Source login branch%d%s", row.Status, row.Body)
		}
		result.rows = append(result.rows, row.Body)
	}
	return result
}

func TestDesktopWhatsAppBlockedPhysicalTLSBeginIgnoresRendererAbortAndJoinsOnServerClose(t *testing.T) {
	entered, peerCanceled := make(chan struct{}), make(chan struct{})
	var once sync.Once
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sw.js" && r.URL.Path != "/ws/chat" {
			t.Errorf("unowned physical path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		once.Do(func() { close(entered) })
		<-r.Context().Done()
		close(peerCanceled)
	}))
	defer upstream.Close()
	s, _ := waRegistrationFactory(t, waRegistrationTransport{upstream})
	peer := httptest.NewServer(s)
	defer peer.Close()
	ctx, disconnect := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, "POST", peer.URL+"/api/adapters/whatsapp/login/start", nil)
	responseDone := make(chan error, 1)
	go func() {
		r, e := peer.Client().Do(request)
		if r != nil {
			r.Body.Close()
		}
		responseDone <- e
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("actual SDK physical TLS version request did not reach peer")
	}
	disconnect()
	if e := <-responseDone; e == nil {
		t.Fatal("renderer request unexpectedly completed")
	}
	select {
	case <-peerCanceled:
		t.Fatal("renderer disconnect canceled accepted physical TLS request")
	default:
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := s.Close(closeCtx); e != nil {
		t.Fatal("native lifecycle did not join accepted Connect", e)
	}
	select {
	case <-peerCanceled:
	case <-closeCtx.Done():
		t.Fatal("physical peer did not observe server cancellation")
	}
	if e := s.Close(context.Background()); e != nil {
		t.Fatal("shared second Close", e)
	}
}
