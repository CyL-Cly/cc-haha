package whatsappcandidate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	sig "go.mau.fi/libsignal/protocol"
	"go.mau.fi/libsignal/session"
	"go.mau.fi/whatsmeow/store"
)

func TestActualClientDeviceCipherKeepsOriginalArchivedSessionAndCachedBulkCommit(t *testing.T) {
	ctx := session.WithNodeSessionSemantics(context.Background())
	source := actualNodePeer(t, map[string]any{"op": "setup"})
	b := nodeObj(source["b"])
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "creds.json"), b["creds"], 0600); e != nil {
		t.Fatal(e)
	}
	for name, v := range nodeObj(nodeObj(b["values"])["session"]) {
		if e := os.WriteFile(filepath.Join(dir, "session-"+name+".json"), v, 0600); e != nil {
			t.Fatal(e)
		}
	}
	device, _, e := OpenNodeDevice(dir, nil)
	if e != nil {
		t.Fatal(e)
	}
	address := sig.NewSignalAddress("111", 0)
	cipher := session.NewCipher(session.NewBuilderFromSignal(device, address, store.SignalProtobufSerializer), address)
	var packets []nodeObject
	_ = json.Unmarshal(source["packets"], &packets)
	decrypt := func(ctx context.Context, p nodeObject) string {
		m, e := sig.NewSignalMessageFromBytes(nodeBytes(p["ciphertext"]), store.SignalProtobufSerializer.SignalMessage)
		if e != nil {
			t.Fatal(e)
		}
		plain, e := cipher.Decrypt(ctx, m)
		if e != nil {
			t.Fatal("actual Device cipher", e)
		}
		return string(plain)
	}
	if decrypt(ctx, packets[4]) != "NODE_MESSAGE_4" {
		t.Fatal("initial native packet")
	}
	reply, e := cipher.Encrypt(ctx, []byte("DEVICE_BEFORE_REKEY"))
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(dir, "session-111.0.json"))
	if e != nil {
		t.Fatal(e)
	}
	values := nodeObj(b["values"])
	values["session"] = nodeJSON(nodeObject{"111.0": raw})
	b["values"] = nodeJSON(values)
	source = actualNodePeer(t, map[string]any{"op": "continue", "round": 1, "rekey": true, "a": source["a"], "b": b, "replies": []map[string]string{{"type": "msg", "ciphertext": base64.StdEncoding.EncodeToString(reply.Serialize())}}})
	b = nodeObj(source["b"])
	for name, v := range nodeObj(nodeObj(b["values"])["session"]) {
		if e = os.WriteFile(filepath.Join(dir, "session-"+name+".json"), v, 0600); e != nil {
			t.Fatal(e)
		}
	}
	// The original Node peer re-keyed in a separate process and wrote the old
	// directory. Reopen a real device to import that process's state; the
	// Source key cache intentionally hides external edits for five minutes.
	device, _, e = OpenNodeDevice(dir, nil)
	if e != nil {
		t.Fatal(e)
	}
	cipher = session.NewCipher(session.NewBuilderFromSignal(device, address, store.SignalProtobufSerializer), address)
	_, cached, e := device.WithCachedSessions(ctx, []string{"111:0"})
	if e != nil {
		t.Fatal(e)
	}
	if decrypt(cached, packets[2]) != "NODE_MESSAGE_2" {
		t.Fatal("late archived packet")
	}
	if e = device.PutCachedSessions(cached); e != nil {
		t.Fatal("actual bulk commit", e)
	}
	raw, e = os.ReadFile(filepath.Join(dir, "session-111.0.json"))
	if e != nil {
		t.Fatal(e)
	}
	entries := nodeObj(nodeObj(raw)["_sessions"])
	open := 0
	for _, v := range entries {
		if nodeNumber(nodeObj(nodeObj(v)["indexInfo"])["closed"]) == -1 {
			open++
		}
	}
	if len(entries) != 2 || open != 1 {
		t.Fatal("old archive/open state must remain", len(entries), open)
	}
	var next []nodeObject
	_ = json.Unmarshal(source["packets"], &next)
	if decrypt(ctx, next[4]) != "NODE_RATCHET_1_4" {
		t.Fatal("original active session lost")
	}
	reply, e = cipher.Encrypt(ctx, []byte("DEVICE_AFTER_ARCHIVE"))
	if e != nil {
		t.Fatal(e)
	}
	source = actualNodePeer(t, map[string]any{"op": "continue", "round": 2, "a": source["a"], "b": b, "replies": []map[string]string{{"type": "msg", "ciphertext": base64.StdEncoding.EncodeToString(reply.Serialize())}}})
	var plain []string
	_ = json.Unmarshal(source["plain"], &plain)
	if len(plain) != 1 || plain[0] != "DEVICE_AFTER_ARCHIVE" {
		t.Fatal("actual original Node could not consume native reply", plain)
	}
}
