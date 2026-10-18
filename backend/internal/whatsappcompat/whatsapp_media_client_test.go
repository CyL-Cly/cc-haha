package whatsappcandidate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.mau.fi/whatsmeow"
	wab "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.mau.fi/whatsmeow/util/keys"
)

func actualMediaSource(t *testing.T, input any) nodeObject {
	t.Helper()
	home := t.TempDir()
	ctx, stop := context.WithTimeout(context.Background(), 40*time.Second)
	defer stop()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("node_media.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "PATH=/opt/homebrew/bin:/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(nodeJSON(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal("actual media Source", e, stderr.String())
	}
	return nodeObj(out)
}

type mediaTLSRoute struct{ peer *httptest.Server }

func (r mediaTLSRoute) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "web.whatsapp.com" && req.URL.Host != "mmg.whatsapp.net" {
		return nil, fmt.Errorf("unowned media host %s", req.URL.Host)
	}
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = "https"
	u.Host = r.peer.Listener.Addr().String()
	copy.URL = &u
	copy.Header.Set("X-Fixture-Logical-URL", req.URL.String())
	return r.peer.Client().Transport.RoundTrip(copy)
}

func TestActualSourceMediaMetadataUploadTLSNoiseSignal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	source := actualNodePeer(t, map[string]any{"op": "setup"})
	a := source["a"]
	b := nodeObj(source["b"])
	creds := nodeObj(b["creds"])
	creds["me"] = nodeJSON(map[string]any{"id": "222:3@s.whatsapp.net", "lid": "99222:3@lid", "name": "private-media"})
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "creds.json"), nodeJSON(creds), 0600); e != nil {
		t.Fatal(e)
	}
	for name, v := range nodeObj(nodeObj(b["values"])["session"]) {
		if e := os.WriteFile(filepath.Join(dir, "session-"+name+".json"), v, 0600); e != nil {
			t.Fatal(e)
		}
	}
	root, intermediate, static := keys.NewKeyPair(), keys.NewKeyPair(), keys.NewKeyPair()
	oldPin := whatsmeow.WACertPubKey
	whatsmeow.WACertPubKey = *root.Pub
	defer func() { whatsmeow.WACertPubKey = oldPin }()
	sent := make(chan wab.Node, 16)
	connected := make(chan struct{}, 1)
	errs := make(chan error, 8)
	var mu sync.Mutex
	uploads := [][]byte{}
	var retryMode atomic.Int32
	retryRequests := make(chan wab.Node, 16)
	retryKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{19}, 32))
	handlers := newMediaFixtureHandlers()
	peer := httptest.NewTLSServer(handlers.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			data, e := io.ReadAll(r.Body)
			if e != nil {
				errs <- e
				return
			}
			if r.TLS == nil || r.Header.Get("Origin") != "https://web.whatsapp.com" {
				errs <- fmt.Errorf("physical TLS/Origin missing")
			}
			mu.Lock()
			uploads = append(uploads, data)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"url":"https://mmg.whatsapp.net/media","direct_path":"/media"}`)
			return
		}
		conn, e := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if e != nil {
			errs <- e
			return
		}
		defer conn.CloseNow()
		wire, p, e := acceptPrivateNoise(ctx, conn, root, intermediate, static)
		if e != nil {
			errs <- e
			return
		}
		if p.GetUsername() != 222 {
			errs <- fmt.Errorf("old Noise login")
		}
		wire.Send(wab.Node{Tag: "success", Attrs: wab.Attrs{"lid": types.JID{User: "99222", Device: 3, Server: types.HiddenUserServer}, "t": time.Now().Unix()}})
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
					users := []wab.Node{}
					list := usync.GetChildByTag("list")
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
				if e := wire.Send(result); e != nil {
					errs <- e
					return
				}
			case "receipt":
				if node.Attrs["type"] == "server-error" {
					retryRequests <- *node
					id := fmt.Sprint(node.Attrs["id"])
					c := node.GetChildByTag("encrypt", "enc_p")
					iv := node.GetChildByTag("encrypt", "enc_iv")
					receipt := actualMediaSource(t, map[string]any{"op": "decodeReceipt", "id": id, "key": retryKey, "ciphertext": base64.StdEncoding.EncodeToString(c.Content.([]byte)), "iv": base64.StdEncoding.EncodeToString(iv.Content.([]byte))})
					if nodeString(receipt["stanzaId"]) != id {
						errs <- fmt.Errorf("original SDK receipt decryption")
					}
					mode := retryMode.Load()
					if mode == 0 {
						continue
					}
					content := []wab.Node{{Tag: "rmr", Attrs: wab.Attrs{"jid": types.NewJID("111", types.DefaultUserServer), "from_me": "false"}}}
					if mode == -2 {
						content = append(content, wab.Node{Tag: "error", Attrs: wab.Attrs{"code": "2"}})
					} else {
						result := mode
						if mode < 0 {
							result = 1
						}
						response := actualMediaSource(t, map[string]any{"op": "retryNotification", "id": id, "key": retryKey, "path": "/fresh", "result": result})
						cipher := nodeBytes(response["ciphertext"])
						if mode == -1 {
							cipher[0] ^= 1
						}
						content = append(content, wab.Node{Tag: "encrypt", Content: []wab.Node{{Tag: "enc_p", Content: cipher}, {Tag: "enc_iv", Content: nodeBytes(response["iv"])}}})
					}
					wire.Send(wab.Node{Tag: "notification", Attrs: wab.Attrs{"id": id, "from": types.NewJID("222", types.DefaultUserServer), "type": "mediaretry", "t": time.Now().Unix()}, Content: content})
				}
			case "message":
				sent <- *node
				wire.Send(wab.Node{Tag: "ack", Attrs: wab.Attrs{"id": node.Attrs["id"], "class": "message", "from": node.Attrs["to"], "t": time.Now().Unix()}})
			}
		}
	})))
	defer func() {
		cancel()
		<-handlers.close(peer)
	}()
	n, e := NewNodeWhatsAppClient(ctx, NodeWhatsAppOptions{AuthDir: dir, HTTPTransport: mediaTLSRoute{peer}, OnEvent: func(v any) {
		if _, ok := v.(*events.Connected); ok {
			connected <- struct{}{}
		}
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close(context.Background())
	if e = n.Connect(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-connected:
	case e := <-errs:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	pixel := image.NewNRGBA(image.Rect(0, 0, 123, 83))
	for y := 0; y < 83; y++ {
		for x := 0; x < 123; x++ {
			pixel.SetNRGBA(x, y, color.NRGBA{uint8(x * 7), uint8(y * 3), uint8(x + y), 137})
		}
	}
	var pngData bytes.Buffer
	png.Encode(&pngData, pixel)
	fixtures := []struct {
		mime string
		data []byte
	}{{"image/png", pngData.Bytes()}, {"audio/wav", mediaWAV()}, {"application/pdf", []byte("private-document")}, {"image/png", []byte("invalid-image")}}
	videoPath := filepath.Join(t.TempDir(), "video.mp4")
	videoCmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=80x48:d=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-y", videoPath)
	if out, e := videoCmd.CombinedOutput(); e != nil {
		t.Fatal("owned video fixture", e, string(out))
	}
	videoData, e := os.ReadFile(videoPath)
	if e != nil {
		t.Fatal(e)
	}
	fixtures = append(fixtures, struct {
		mime string
		data []byte
	}{"video/mp4", videoData})
	n.now = func() time.Time { return time.Unix(1700000000, 0) }
	for index, spec := range fixtures {
		caption := "private caption"
		if index == 2 {
			caption = ""
		}
		original := actualMediaSource(t, map[string]any{"op": "prepare", "data": base64.StdEncoding.EncodeToString(spec.data), "mime": spec.mime, "caption": caption, "name": "private.pdf", "now": int64(1700000000000)})
		want := nodeObj(original["message"])
		// This socket-level test injects a fixture thumbnail producer. The runtime
		// tests independently compare the actual native producer bytes to Sharp.
		imageMetadata := nodeObj(want["imageMessage"])
		n.imageThumbnail = func([]byte) (NodeImageThumbnail, error) {
			if len(imageMetadata["jpegThumbnail"]) == 0 {
				return NodeImageThumbnail{}, fmt.Errorf("fixture image decode failed")
			}
			var width, height uint32
			json.Unmarshal(imageMetadata["width"], &width)
			json.Unmarshal(imageMetadata["height"], &height)
			return NodeImageThumbnail{JPEG: nodeBytes(imageMetadata["jpegThumbnail"]), Width: width, Height: height}, nil
		}
		if _, e = n.SendMedia(ctx, types.NewJID("111", types.DefaultUserServer), spec.data, spec.mime, caption, "private.pdf"); e != nil {
			t.Fatal(e)
		}
		var node wab.Node
		select {
		case node = <-sent:
		case e := <-errs:
			t.Fatal(e)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		var enc wab.Node
		participants := node.GetChildByTag("participants")
		for _, p := range participants.GetChildren() {
			jid, _ := p.Attrs["jid"].(types.JID)
			if jid.User == "111" {
				enc = p.GetChildByTag("enc")
			}
		}
		response := originalMessagePeer(t, map[string]any{"op": "decrypt", "a": a, "type": enc.Attrs["type"], "ciphertext": base64.StdEncoding.EncodeToString(enc.Content.([]byte))})
		a = response["a"]
		got := nodeObj(response["message"])
		field := "documentMessage"
		mediaType := "document"
		if strings.HasPrefix(spec.mime, "image/") {
			field = "imageMessage"
			mediaType = "image"
		} else if strings.HasPrefix(spec.mime, "video/") {
			field = "videoMessage"
			mediaType = "video"
		} else if strings.HasPrefix(spec.mime, "audio/") {
			field = "audioMessage"
			mediaType = "audio"
		}
		g, w := nodeObj(got[field]), nodeObj(want[field])
		key := nodeString(g["mediaKey"])
		if !bytes.Equal(g["mediaKeyTimestamp"], w["mediaKeyTimestamp"]) {
			t.Fatal("actual Source media timestamp differs", string(g["mediaKeyTimestamp"]), string(w["mediaKeyTimestamp"]))
		}
		var timestamp string
		json.Unmarshal(g["mediaKeyTimestamp"], &timestamp)
		if timestamp == "" || timestamp == "0" {
			t.Fatal("actual SDK media timestamp missing")
		}
		mu.Lock()
		upload := append([]byte{}, uploads[len(uploads)-1]...)
		mu.Unlock()
		disk := filepath.Join(t.TempDir(), "uploaded.cipher")
		os.WriteFile(disk, upload, 0600)
		physical, _ := os.ReadFile(disk)
		digest := sha256.Sum256(physical)
		if !bytes.Equal(digest[:], nodeBytes(g["fileEncSha256"])) {
			t.Fatal("actual physical upload cipher hash differs from Signal message")
		}
		decrypted := actualMediaSource(t, map[string]any{"op": "decrypt", "key": key, "type": mediaType, "data": base64.StdEncoding.EncodeToString(physical)})
		if !bytes.Equal(nodeBytes(decrypted["data"]), spec.data) {
			t.Fatal("actual upload does not decrypt in original SDK")
		}
		for _, k := range []string{"url", "directPath", "mediaKey", "fileEncSha256", "mediaKeyTimestamp"} {
			delete(g, k)
			delete(w, k)
		}
		if !bytes.Equal(nodeJSON(g), nodeJSON(w)) {
			t.Fatalf("actual %s metadata Go %s Source %s", spec.mime, nodeJSON(g), nodeJSON(w))
		}
	}
	info := types.MessageInfo{ID: "PRIVATE_MEDIA_RETRY", MessageSource: types.MessageSource{Chat: types.NewJID("111", types.DefaultUserServer), Sender: types.NewJID("111", types.DefaultUserServer)}}
	for _, mode := range []int32{1, 2, 3, 0, -1, -2} {
		retryMode.Store(mode)
		callCtx, stop := context.WithTimeout(ctx, 1500*time.Millisecond)
		if mode == 0 {
			callCtx, stop = context.WithTimeout(ctx, 100*time.Millisecond)
		}
		path, e := n.UpdateMedia(callCtx, info, nodeBytes(nodeJSON(retryKey)))
		stop()
		if mode == 1 {
			if e != nil || path != "/fresh" {
				t.Fatal("native encrypted recovery", path, e)
			}
		} else if e == nil || path != "" {
			t.Fatal("failed/canceled recovery succeeded", mode, path, e)
		}
		select {
		case <-retryRequests:
		case <-ctx.Done():
			t.Fatal("physical encrypted receipt absent")
		}
		n.mu.Lock()
		pending := len(n.mediaRetries)
		n.mu.Unlock()
		if pending != 0 {
			t.Fatal("recovery waiter leaked", mode, pending)
		}
	}
	retryMode.Store(0)
	closeDone := make(chan error, 1)
	go func() { _, e := n.UpdateMedia(ctx, info, nodeBytes(nodeJSON(retryKey))); closeDone <- e }()
	select {
	case <-retryRequests:
	case <-ctx.Done():
		t.Fatal("close receipt absent")
	}
	if e := n.Close(ctx); e != nil {
		t.Fatal("actual media Close", e)
	}
	select {
	case e := <-closeDone:
		if e == nil {
			t.Fatal("closed recovery succeeded")
		}
	case <-ctx.Done():
		t.Fatal("media Close did not join physical work")
	}
	t.Log("original service/SDK metadata; encrypted TLS upload physically saved, native Noise/Signal send decrypted by original Node; media retry SUCCESS/NOT_FOUND/DECRYPTION_ERROR/corruption/error/cancellation/physical Close")
}
func mediaWAV() []byte {
	const rate = 8000
	const samples = rate*3 + rate/2
	data := make([]byte, 44+samples*2)
	copy(data, "RIFF")
	putLE := func(offset, n int) {
		for i := 0; i < 4; i++ {
			data[offset+i] = byte(n >> (i * 8))
		}
	}
	putLE(4, len(data)-8)
	copy(data[8:], "WAVEfmt ")
	putLE(16, 16)
	data[20] = 1
	data[22] = 1
	putLE(24, rate)
	putLE(28, rate*2)
	data[32] = 2
	data[34] = 16
	copy(data[36:], "data")
	putLE(40, samples*2)
	return data
}

var _ = json.RawMessage{}

func TestNativeAudioDurationActualMusicMetadataSource(t *testing.T) {
	fixtures := []struct {
		mime string
		data []byte
	}{{"audio/wav", mediaWAV()}, {"audio/wav", []byte("invalid-audio")}}
	dir := t.TempDir()
	source := filepath.Join(dir, "original.wav")
	if e := os.WriteFile(source, mediaWAV(), 0600); e != nil {
		t.Fatal(e)
	}
	for _, spec := range []struct{ name, mime, codec string }{{"audio.mp3", "audio/mpeg", "libmp3lame"}, {"audio.ogg", "audio/ogg", "libopus"}, {"audio.flac", "audio/flac", "flac"}, {"audio.aac", "audio/aac", "aac"}, {"audio.m4a", "audio/mp4", "aac"}, {"audio-vorbis.ogg", "audio/ogg", "vorbis"}, {"audio.aiff", "audio/aiff", "pcm_s16be"}, {"audio.wma", "audio/x-ms-wma", "wmav2"}, {"audio.wv", "audio/wavpack", "wavpack"}, {"audio.mka", "audio/x-matroska", "flac"}, {"audio.webm", "audio/webm", "libopus"}, {"audio-flac.oga", "audio/ogg", "flac"}, {"audio-alaw.wav", "audio/wav", "pcm_alaw"}} {
		output := filepath.Join(dir, spec.name)
		sampleRate := "44100"
		if spec.codec == "libopus" {
			sampleRate = "48000"
		}
		cmd := exec.Command("ffmpeg", "-v", "error", "-i", source, "-ar", sampleRate, "-ac", "2", "-c:a", spec.codec, "-strict", "-2", "-y", output)
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatal("owned audio fixture", e, string(out))
		}
		data, e := os.ReadFile(output)
		if e != nil {
			t.Fatal(e)
		}
		fixtures = append(fixtures, struct {
			mime string
			data []byte
		}{spec.mime, data})
	}
	for _, fixture := range fixtures {
		want := nodeObj(actualMediaSource(t, map[string]any{"op": "prepare", "mime": fixture.mime, "data": base64.StdEncoding.EncodeToString(fixture.data)})["message"])
		audio := nodeObj(want["audioMessage"])
		seconds, e := NodeAudioDuration(fixture.data)
		if len(audio["seconds"]) == 0 {
			if e == nil {
				t.Fatal("Source omitted malformed metadata but native supplied", seconds)
			}
			continue
		}
		var expected uint32
		if e1 := json.Unmarshal(audio["seconds"], &expected); e1 != nil {
			t.Fatal(e1)
		}
		if e != nil || seconds != expected {
			t.Fatalf("actual music-metadata %s native=%d Source=%d err=%v", fixture.mime, seconds, expected, e)
		}
	}
}
