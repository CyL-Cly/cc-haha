package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	sig "go.mau.fi/libsignal/protocol"
	"go.mau.fi/libsignal/session"
	"go.mau.fi/whatsmeow/store"
)

func TestForkActualFirstOutOfOrderPreKeyColdLatePacketsAndOriginalFileConsumer(t *testing.T) {
	source := originalForkEdges(t, map[string]any{"op": "prekey"})
	late := actualNodePeer(t, map[string]any{"op": "continue", "round": 1, "a": source["a"], "b": source["b"], "replies": []any{}})
	var packets []nodeObject
	if err := json.Unmarshal(late["packets"], &packets); err != nil {
		t.Fatal(err)
	}
	packet2 := packets[1] // first generated packet was counter0; continuation is 1..5.
	raw2 := nodeBytes(packet2["ciphertext"])
	originalB := originalForkEdges(t, map[string]any{"op": "decrypt", "now": 3333, "b": source["b"], "type": "pkmsg", "ciphertext": base64.StdEncoding.EncodeToString(raw2)})
	if nodeString(originalB["plain"]) != "NODE_RATCHET_1_1" {
		t.Fatal("actual Source initial counter2", nodeString(originalB["error"]))
	}
	b := nodeObj(source["b"])
	_, _, dir := forkDeviceFromOriginal(t, b)
	addr := sig.NewSignalAddress("111", 0)
	path := filepath.Join(dir, "session-111.0.json")
	now := func() time.Time { return time.UnixMilli(3333) }
	newCipher := func() (*session.Cipher, context.Context) {
		device, n, err := OpenNodeDevice(dir, now)
		if err != nil {
			t.Fatal(err)
		}
		return session.NewCipher(session.NewBuilderFromSignal(device, addr, store.SignalProtobufSerializer), addr), session.WithNodeSessionTransactions(context.Background(), n.AcquireSessions)
	}
	parse := func(raw []byte) *sig.PreKeySignalMessage {
		p, err := sig.NewPreKeySignalMessageFromBytes(raw, store.SignalProtobufSerializer.PreKeySignalMessage, store.SignalProtobufSerializer.SignalMessage)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	cipher, ctx := newCipher()
	// A bad MAC at this initial boundary must not create even a partial session.
	tampered := append([]byte(nil), raw2...)
	tampered[len(tampered)-1] ^= 0x10
	if _, err := cipher.DecryptMessage(ctx, parse(tampered)); err == nil {
		t.Fatal("initial counter2 bad MAC accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("initial bad MAC persisted state", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := cipher.DecryptMessage(canceled, parse(raw2)); !errors.Is(err, context.Canceled) {
		t.Fatal("initial counter2 canceled", err)
	}
	plain, err := cipher.DecryptMessage(ctx, parse(raw2))
	if err != nil || string(plain) != "NODE_RATCHET_1_1" {
		t.Fatal("actual first out-of-order PreKey", string(plain), err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(saved, []byte("NodeSeed")) {
		t.Fatal("native ephemeral seed field escaped into original Node file schema")
	}
	// The actual Source auth-store producer receives the entire persisted record,
	// writes its physical file, and must preserve every byte of that same object.
	persist := originalForkEdges(t, map[string]any{"op": "persist", "record": json.RawMessage(saved)})
	if !bytes.Equal([]byte(nodeString(persist["raw"])), saved) {
		t.Fatal("full original physical auth-store JSON bytes differ")
	}
	// Source independently initialized counter2. Both retained skipped seeds must
	// be identical even though each cryptographic runtime chose its own new
	// random outgoing ratchet key. Do not normalize or compare those random keys.
	sourceEntries := nodeObj(nodeObj(nodeObj(originalB["b"])["values"])["session"])
	goEntries := nodeObj(nodeObj(saved)["_sessions"])
	originalEntries := nodeObj(nodeObj(sourceEntries["111.0"])["_sessions"])
	for base, entry := range goEntries {
		goChains := nodeObj(nodeObj(entry)["_chains"])
		sourceChains := nodeObj(nodeObj(originalEntries[base])["_chains"])
		matched := 0
		for chain, raw := range goChains {
			if nodeNumber(nodeObj(raw)["chainType"]) != 2 {
				continue
			}
			got := nodeObj(nodeObj(raw)["messageKeys"])
			want := nodeObj(nodeObj(sourceChains[chain])["messageKeys"])
			if len(got) != 2 || !bytes.Equal(nodeJSON(got), nodeJSON(want)) {
				t.Fatal("actual initial skipped seeds differ from original Node", len(got), len(want))
			}
			matched++
		}
		if matched != 1 {
			t.Fatal("initial receiving chain missing", matched)
		}
	}
	// Both late packets are consumed from a freshly reopened real auth directory.
	// Source is a separate process reading that same complete native record.
	latePackets := []nodeObject{nodeObj(source["packet"]), packets[0]}
	for i, packet := range latePackets {
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		values := nodeObj(b["values"])
		values["session"] = nodeJSON(nodeObject{"111.0": before})
		b["values"] = nodeJSON(values)
		raw := nodeBytes(packet["ciphertext"])
		proof := originalForkEdges(t, map[string]any{"op": "decrypt", "now": 3333, "b": b, "type": "pkmsg", "ciphertext": base64.StdEncoding.EncodeToString(raw)})
		expected := "PRIVATE_FIRST_PREKEY"
		if i == 1 {
			expected = "NODE_RATCHET_1_0"
		}
		if nodeString(proof["plain"]) != expected {
			t.Fatal("original process could not consume native skipped record", i, nodeString(proof["error"]))
		}
		cipher, ctx = newCipher()
		plain, err = cipher.DecryptMessage(ctx, parse(raw))
		if err != nil || string(plain) != expected {
			t.Fatal("cold late PreKey", i, string(plain), err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		persisted := originalForkEdges(t, map[string]any{"op": "persist", "record": json.RawMessage(after)})
		if !bytes.Equal([]byte(nodeString(persisted["raw"])), after) {
			t.Fatal("late full Source physical JSON differs", i)
		}
		if _, err = cipher.DecryptMessage(ctx, parse(raw)); err == nil {
			t.Fatal("late repeated PreKey authenticated twice", i)
		}
		unchanged, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, unchanged) {
			t.Fatal("late replay changed actual file", i, err)
		}
	}
	cipher, ctx = newCipher()
	reply, err := cipher.Encrypt(ctx, []byte("PRIVATE_AFTER_INITIAL_OUT_OF_ORDER"))
	if err != nil {
		t.Fatal(err)
	}
	proof := actualNodePeer(t, map[string]any{"op": "continue", "round": 2, "a": late["a"], "b": late["b"], "replies": []map[string]string{{"type": "msg", "ciphertext": base64.StdEncoding.EncodeToString(reply.Serialize())}}})
	var plaintext []string
	if err = json.Unmarshal(proof["plain"], &plaintext); err != nil || len(plaintext) != 1 || plaintext[0] != "PRIVATE_AFTER_INITIAL_OUT_OF_ORDER" {
		t.Fatal("real original process rejected cold native reply", plaintext, err)
	}
}

func TestForkActualNonNodeCipherDefaultSerializedKeysRetainPinnedShape(t *testing.T) {
	source := actualNodePeer(t, map[string]any{"op": "setup"})
	device, _, _ := forkDeviceFromOriginal(t, nodeObj(source["b"]))
	addr := sig.NewSignalAddress("111", 0)
	cipher := session.NewCipher(session.NewBuilderFromSignal(device, addr, store.SignalProtobufSerializer), addr)
	var packets []nodeObject
	if err := json.Unmarshal(source["packets"], &packets); err != nil {
		t.Fatal(err)
	}
	p, err := sig.NewSignalMessageFromBytes(nodeBytes(packets[4]["ciphertext"]), store.SignalProtobufSerializer.SignalMessage)
	if err != nil {
		t.Fatal(err)
	}
	// Use the ordinary default library context, with no Node semantics or hook.
	ctx := context.Background()
	record, err := device.LoadSession(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := cipher.DecryptWithRecord(ctx, record, p)
	if err != nil || string(plain) != "NODE_MESSAGE_4" {
		t.Fatal("ordinary default library decrypt", string(plain), err)
	}
	raw := record.Serialize()
	if bytes.Contains(raw, []byte("NodeSeed")) {
		t.Fatal("non-Node serializer gained optional metadata")
	}
	shape, err := store.SignalProtobufSerializer.Session.Deserialize(raw)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, chain := range shape.SessionState.ReceiverChains {
		for _, key := range chain.MessageKeys {
			encoded, err := json.Marshal(key)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if len(fields) != 4 || fields["CipherKey"] == nil || fields["MacKey"] == nil || fields["IV"] == nil || fields["Index"] == nil {
				t.Fatal("default pinned skipped-key JSON shape changed")
			}
			if len(key.NodeSeed) != 0 {
				t.Fatal("non-Node retained Source seed")
			}
			count++
		}
	}
	if count != 4 {
		t.Fatal("default decrypt did not exercise skipped keys", count)
	}
}

func TestForkActualTypedSessionStoreRejectsForgedRetainedSeedWithoutChangingOldFile(t *testing.T) {
	source := actualNodePeer(t, map[string]any{"op": "setup"})
	device, n, dir := forkDeviceFromOriginal(t, nodeObj(source["b"]))
	addr := sig.NewSignalAddress("111", 0)
	path := filepath.Join(dir, "session-111.0.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	record, err := device.LoadSession(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	cipher := session.NewCipher(session.NewBuilderFromSignal(device, addr, store.SignalProtobufSerializer), addr)
	var packets []nodeObject
	if err = json.Unmarshal(source["packets"], &packets); err != nil {
		t.Fatal(err)
	}
	packet, err := sig.NewSignalMessageFromBytes(nodeBytes(packets[4]["ciphertext"]), store.SignalProtobufSerializer.SignalMessage)
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := cipher.DecryptWithRecord(session.WithNodeSessionSemantics(context.Background()), record, packet)
	if err != nil || string(plain) != "NODE_MESSAGE_4" {
		t.Fatal("actual retained key producer", string(plain), err)
	}
	shape, err := store.SignalProtobufSerializer.Session.Deserialize(record.Serialize())
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	for _, chain := range shape.SessionState.ReceiverChains {
		for _, key := range chain.MessageKeys {
			if len(key.NodeSeed) > 0 {
				key.NodeSeed[0] ^= 0x10
				changed = true
				break
			}
		}
		if changed {
			break
		}
	}
	if !changed {
		t.Fatal("actual opted-in producer omitted retained seed")
	}
	// Exercise the real exported Device SessionStore boundary with the exact
	// native serializer envelope, not a fabricated private exporter invocation.
	invalid := store.SignalProtobufSerializer.Session.Serialize(shape)
	err = n.PutSession(context.Background(), "111:0", invalid)
	if err == nil || err.Error() != "retained Node skipped seed no longer matches native key" {
		t.Fatal("forged retained seed accepted", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("invalid retained seed changed old auth file", err)
	}
}
