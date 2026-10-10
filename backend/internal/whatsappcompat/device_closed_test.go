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

func TestActualOriginalClosedOnlyRecordDecryptsOnConcreteClientDevice(t *testing.T) {
	source := actualNodePeer(t, map[string]any{"op": "setup"})
	packetSource := originalMessagePeer(t, map[string]any{"op": "encrypt", "a": source["a"], "message": map[string]any{"conversation": "PRIVATE_CLOSED_ONLY_PACKET"}})
	packet := nodeObj(packetSource["packet"])
	proof := originalMessagePeer(t, map[string]any{"op": "decrypt", "a": source["b"], "closedOnly": true, "jid": "111@s.whatsapp.net", "type": nodeString(packet["type"]), "ciphertext": base64.StdEncoding.EncodeToString(nodeBytes(packet["ciphertext"]))})
	if nodeString(nodeObj(proof["message"])["conversation"]) != "PRIVATE_CLOSED_ONLY_PACKET" {
		t.Fatal("original closed-only decrypt did not succeed")
	}
	b := nodeObj(source["b"])
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "creds.json"), b["creds"], 0600); e != nil {
		t.Fatal(e)
	}
	for name, raw := range nodeObj(nodeObj(b["values"])["session"]) {
		o := nodeObj(raw)
		entries := nodeObj(o["_sessions"])
		for id, entry := range entries {
			state := nodeObj(entry)
			index := nodeObj(state["indexInfo"])
			index["closed"] = nodeJSON(3333)
			state["indexInfo"] = nodeJSON(index)
			entries[id] = nodeJSON(state)
		}
		o["_sessions"] = nodeJSON(entries)
		if e := os.WriteFile(filepath.Join(dir, "session-"+name+".json"), nodeJSON(o), 0600); e != nil {
			t.Fatal(e)
		}
	}
	device, _, e := OpenNodeDevice(dir, nil)
	if e != nil {
		t.Fatal(e)
	}
	addr := sig.NewSignalAddress("111", 0)
	cipher := session.NewCipher(session.NewBuilderFromSignal(device, addr, store.SignalProtobufSerializer), addr)
	ctx := session.WithNodeSessionSemantics(context.Background())
	m, e := sig.NewSignalMessageFromBytes(nodeBytes(packet["ciphertext"]), store.SignalProtobufSerializer.SignalMessage)
	if e != nil {
		t.Fatal(e)
	}
	plain, e := cipher.Decrypt(ctx, m)
	if e != nil {
		t.Fatal("actual client closed-only", e)
	}
	if len(plain) == 0 {
		t.Fatal("empty native plaintext")
	}
	if _, e = cipher.Encrypt(ctx, []byte("must-not-open")); e == nil || e.Error() != "No open session" {
		t.Fatal("closed-only outbound Source boundary", e)
	}
	raw, e := os.ReadFile(filepath.Join(dir, "session-111.0.json"))
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range nodeObj(nodeObj(raw)["_sessions"]) {
		if nodeNumber(nodeObj(nodeObj(entry)["indexInfo"])["closed"]) != 3333 {
			t.Fatal("closed original record opened unexpectedly")
		}
	}
	var structure map[string]any
	if e = json.Unmarshal(raw, &structure); e != nil {
		t.Fatal(e)
	}
	if len(structure) == 0 {
		t.Fatal("old closed state lost")
	}
}
