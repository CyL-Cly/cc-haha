package imruntime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
)

// req_id is a random transport correlation value. All other failure fields and
// the resulting original SDK error text remain intact in the comparison.
func wecomUploadComparableError(e error) string {
	if e == nil {
		return ""
	}
	// Strip only the generated headers object from the raw ACK serialized in the
	// original upload error; it cannot be predicted across two real transports.
	return wecomUploadACKHeaders.ReplaceAllString(e.Error(), "")
}

var wecomUploadACKHeaders = regexp.MustCompile(`"headers":\{"req_id":"[^"]*"\},`)

func TestWecomOriginalUploadEmptyRetryAndChunkCapsActualTLS(t *testing.T) {
	cases := []map[string]any{{"size": 0}, {"size": 37, "failCount": 2}, {"size": 31, "failCount": 3}, {"size": 5*512*1024 + 1}, {"size": 11*512*1024 + 1}, {"size": 101 * 512 * 1024}}
	want := runWecomSource(t, map[string]any{"uploadCases": cases})
	wantCases := collectJSONArray(want.Get("uploadCases"))
	results := []protocol.JSONObject{}
	for index, config := range cases {
		var mu sync.Mutex
		calls := []protocol.JSONObject{}
		counts := map[int]int{}
		failCount, _ := config["failCount"].(int)
		peer, dial := wecomPrivatePeer(t, func(conn *websocket.Conn) {
			defer conn.Close()
			for {
				var wire string
				if websocket.Message.Receive(conn, &wire) != nil {
					return
				}
				frame := object([]byte(wire))
				cmd := frame.String("cmd")
				if cmd == "aibot_subscribe" {
					_ = wecomAck(conn, frame, nil)
					continue
				}
				body := object(frame.Get("body"))
				observed := object(raw(body))
				var chunkIndex int
				_ = json.Unmarshal(body.Get("chunk_index"), &chunkIndex)
				if v := body.Get("base64_data"); len(v) > 0 {
					bytes, e := base64.StdEncoding.DecodeString(body.String("base64_data"))
					if e != nil {
						return
					}
					sum := sha256.Sum256(bytes)
					observed.Set("base64_data", raw(wecomJSONObject(wecomField("length", len(body.String("base64_data"))), wecomField("sha256", hex.EncodeToString(sum[:])))))
				}
				mu.Lock()
				calls = append(calls, wecomJSONObject(wecomField("cmd", cmd), wecomField("body", observed)))
				if cmd == "aibot_upload_media_chunk" {
					counts[chunkIndex]++
				}
				n := counts[chunkIndex]
				mu.Unlock()
				switch cmd {
				case "aibot_upload_media_init":
					_ = wecomAck(conn, frame, map[string]string{"upload_id": "upload"})
				case "aibot_upload_media_finish":
					_ = wecomAck(conn, frame, map[string]string{"media_id": "media", "type": "image", "created_at": "frozen"})
				default:
					if n <= failCount {
						_ = websocket.Message.Send(conn, string(raw(wecomJSONObject(wecomField("headers", object(frame.Get("headers"))), wecomField("errcode", 7), wecomField("errmsg", "chunk rejected")))))
					} else {
						_ = wecomAck(conn, frame, nil)
					}
				}
			}
		})
		ready := make(chan struct{})
		client, e := NewWecomClient(context.Background(), WecomClientOptions{Dial: dial, OnAuthenticated: func() { close(ready) }})
		if e != nil {
			t.Fatal(e)
		}
		client.Start()
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("auth")
		}
		data := make([]byte, config["size"].(int))
		for i := range data {
			data[i] = byte(i % 251)
		}
		got, e := client.UploadMedia(context.Background(), data, NewText("image"), NewText("image.png"))
		client.Close()
		peer.Close()
		mu.Lock()
		actualCalls := append([]protocol.JSONObject{}, calls...)
		mu.Unlock()
		result := wecomCaptured(got, e)
		if e != nil {
			result = wecomJSONObject(wecomField("error", wecomUploadComparableError(e)))
		}
		// Multiple independent chunk request IDs permit arbitrary physical arrival
		// order. Compare the complete body for each index and each retry attempt.
		ordered := func(list []protocol.JSONObject) []protocol.JSONObject {
			out := []protocol.JSONObject{}
			chunks := map[int][]protocol.JSONObject{}
			finish := []protocol.JSONObject{}
			for _, call := range list {
				if call.String("cmd") == "aibot_upload_media_chunk" {
					var i int
					_ = json.Unmarshal(object(call.Get("body")).Get("chunk_index"), &i)
					chunks[i] = append(chunks[i], call)
				} else if call.String("cmd") == "aibot_upload_media_init" {
					out = append(out, call)
				} else {
					finish = append(finish, call)
				}
			}
			for i := 0; i < len(chunks); i++ {
				out = append(out, chunks[i]...)
			}
			return append(out, finish...)
		}
		wantCase := wantCases[index]
		wantCase.Set("calls", raw(ordered(collectJSONArray(wantCase.Get("calls")))))
		results = append(results, wecomJSONObject(wecomField("calls", ordered(actualCalls)), wecomField("result", result)))
		exactJSON(t, raw(results[len(results)-1]), raw(wantCase), fmt.Sprintf("upload case%d", index))
	}
}
