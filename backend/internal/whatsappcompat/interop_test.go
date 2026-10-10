package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	sig "go.mau.fi/libsignal/protocol"
	"go.mau.fi/libsignal/session"
	"go.mau.fi/whatsmeow/store"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func actualNodePeer(t *testing.T, input any) nodeObject {
	t.Helper()
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("node_peer.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(nodeJSON(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatalf("actual Node peer %v %s", e, stderr.String())
	}
	return nodeObj(data)
}
func TestActualOriginalNodeAndGoSignalManyRatchetsOutOfOrderAndOldStore(t *testing.T) {
	ctx := context.Background()
	source := actualNodePeer(t, map[string]any{"op": "setup"})
	b := nodeObj(source["b"])
	sessions := nodeObj(nodeObj(b["values"])["session"])
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "creds.json"), b["creds"], 0600); e != nil {
		t.Fatal(e)
	}
	native := &NodeSignalStore{Dir: dir, Creds: nodeObj(b["creds"]), Sessions: map[string]json.RawMessage{}, Now: func() time.Time { return time.UnixMilli(3333) }}
	for name, v := range sessions {
		o := nodeObj(v)
		o["futureRoot"] = nodeJSON(map[string]any{"keep": true})
		entries := nodeObj(o["_sessions"])
		for id, v := range entries {
			entry := nodeObj(v)
			entry["futureEntry"] = nodeJSON("untouched")
			entries[id] = nodeJSON(entry)
		}
		o["_sessions"] = nodeJSON(entries)
		v = nodeJSON(o)
		native.Sessions[name] = v
		if e := os.WriteFile(native.nodePath(name), v, 0600); e != nil {
			t.Fatal(e)
		}
	}
	address := sig.NewSignalAddress("111", 0)
	cipher := session.NewCipherFromSession(address, native, nil, native, store.SignalProtobufSerializer.PreKeySignalMessage, store.SignalProtobufSerializer.SignalMessage)
	for round := 0; round < 4; round++ {
		var packets []nodeObject
		if e := json.Unmarshal(source["packets"], &packets); e != nil {
			t.Fatal(e)
		}
		if len(packets) != 5 {
			t.Fatal("original five packets absent")
		}
		for _, i := range []int{4, 0, 2, 1, 3} {
			p := packets[i]
			if nodeString(p["type"]) != "msg" {
				t.Fatal("original established session produced unsupported prekey", nodeString(p["type"]))
			}
			rawPacket := nodeBytes(p["ciphertext"])
			message, e := sig.NewSignalMessageFromBytes(rawPacket, store.SignalProtobufSerializer.SignalMessage)
			if e != nil {
				t.Fatal(e)
			}
			text, e := native.DecryptNodeMessage(ctx, address, cipher, message)
			if e != nil {
				t.Fatalf("round%d packet%d actual native decrypt: %v", round, i, e)
			}
			want := fmt.Sprintf("NODE_MESSAGE_%d", i)
			if round > 0 {
				want = fmt.Sprintf("NODE_RATCHET_%d_%d", round, i)
			}
			if string(text) != want {
				t.Fatalf("native plaintext %q want%q", text, want)
			}
		}
		replies := []map[string]string{}
		for i := 0; i < 2; i++ {
			message, e := native.EncryptNodeMessage(ctx, address, cipher, []byte(fmt.Sprintf("GO_REPLY_%d_%d", round, i)))
			if e != nil {
				t.Fatalf("actual native reply %v", e)
			}
			if message.Type() != sig.WHISPER_TYPE {
				t.Fatal("unexpected native prekey reply")
			}
			replies = append(replies, map[string]string{"type": "msg", "ciphertext": base64.StdEncoding.EncodeToString(message.Serialize())})
		}
		persisted, e := os.ReadFile(native.nodePath("111.0"))
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Contains(persisted, []byte(`"futureRoot":{"keep":true}`)) || !bytes.Contains(persisted, []byte(`"futureEntry":"untouched"`)) {
			t.Fatal("old unknown metadata lost")
		}
		cold, e := OpenNodeSignalDirectory(dir, native.Now)
		if e != nil {
			t.Fatal(e)
		}
		if _, e := cold.LoadSession(ctx, address); e != nil {
			t.Fatal("actual cold old-file restore", e)
		}
		native = cold
		cipher = session.NewCipherFromSession(address, native, nil, native, store.SignalProtobufSerializer.PreKeySignalMessage, store.SignalProtobufSerializer.SignalMessage)
		native.Sessions["111.0"] = persisted
		bValues := nodeObj(b["values"])
		bValues["session"] = nodeJSON(native.Sessions)
		b["values"] = nodeJSON(bValues)
		source = actualNodePeer(t, map[string]any{"op": "continue", "round": round + 1, "a": source["a"], "b": b, "replies": replies})
		var plain []string
		_ = json.Unmarshal(source["plain"], &plain)
		if len(plain) != 2 || plain[0] != fmt.Sprintf("GO_REPLY_%d_0", round) || plain[1] != fmt.Sprintf("GO_REPLY_%d_1", round) {
			t.Fatal("original Node reply decrypt failed", plain)
		}
		b = nodeObj(source["b"])
	}
}

func TestActualNodeAndGoArchiveRetainsLateSkippedPacketAfterNewPrekeySession(t *testing.T) {
	ctx := context.Background()
	source := actualNodePeer(t, map[string]any{"op": "setup"})
	b := nodeObj(source["b"])
	s := &NodeSignalStore{Creds: nodeObj(b["creds"]), Sessions: map[string]json.RawMessage{}, Now: func() time.Time { return time.UnixMilli(3333) }}
	for name, v := range nodeObj(nodeObj(b["values"])["session"]) {
		s.Sessions[name] = v
	}
	addr := sig.NewSignalAddress("111", 0)
	cipher := session.NewCipherFromSession(addr, s, nil, s, store.SignalProtobufSerializer.PreKeySignalMessage, store.SignalProtobufSerializer.SignalMessage)
	var packets []nodeObject
	_ = json.Unmarshal(source["packets"], &packets)
	decrypt := func(packet nodeObject) string {
		m, e := sig.NewSignalMessageFromBytes(nodeBytes(packet["ciphertext"]), store.SignalProtobufSerializer.SignalMessage)
		if e != nil {
			t.Fatal(e)
		}
		plain, e := s.DecryptNodeMessage(ctx, addr, cipher, m)
		if e != nil {
			t.Fatal(e)
		}
		return string(plain)
	}
	if decrypt(packets[4]) != "NODE_MESSAGE_4" {
		t.Fatal("original last packet absent")
	}
	reply, e := s.EncryptNodeMessage(ctx, addr, cipher, []byte("GO_BEFORE_REKEY"))
	if e != nil {
		t.Fatal(e)
	}
	values := nodeObj(b["values"])
	values["session"] = nodeJSON(s.Sessions)
	b["values"] = nodeJSON(values)
	source = actualNodePeer(t, map[string]any{"op": "continue", "round": 1, "rekey": true, "a": source["a"], "b": b, "replies": []map[string]string{{"type": "msg", "ciphertext": base64.StdEncoding.EncodeToString(reply.Serialize())}}})
	b = nodeObj(source["b"])
	for name, v := range nodeObj(nodeObj(b["values"])["session"]) {
		s.Sessions[name] = v
	}
	root := nodeObj(s.Sessions["111.0"])
	if len(nodeObj(root["_sessions"])) != 2 {
		t.Fatal("actual Source prekey did not archive old state")
	}
	if decrypt(packets[2]) != "NODE_MESSAGE_2" {
		t.Fatal("archived skipped packet failed")
	}
	var fresh []nodeObject
	_ = json.Unmarshal(source["packets"], &fresh)
	if decrypt(fresh[4]) != "NODE_RATCHET_1_4" {
		t.Fatal("fresh session unavailable after archived decrypt")
	}
	reply, e = s.EncryptNodeMessage(ctx, addr, cipher, []byte("GO_AFTER_ARCHIVE"))
	if e != nil {
		t.Fatal(e)
	}
	values = nodeObj(b["values"])
	values["session"] = nodeJSON(s.Sessions)
	b["values"] = nodeJSON(values)
	next := actualNodePeer(t, map[string]any{"op": "continue", "round": 2, "a": source["a"], "b": b, "replies": []map[string]string{{"type": "msg", "ciphertext": base64.StdEncoding.EncodeToString(reply.Serialize())}}})
	var plain []string
	_ = json.Unmarshal(next["plain"], &plain)
	if len(plain) != 1 || plain[0] != "GO_AFTER_ARCHIVE" {
		t.Fatal("actual Node reply after old archive", plain)
	}
}

func TestActualNativeSignalTamperedPacketDoesNotMutateOldStore(t *testing.T) {
	ctx := context.Background()
	source := actualNodePeer(t, map[string]any{"op": "setup"})
	b := nodeObj(source["b"])
	s := &NodeSignalStore{Creds: nodeObj(b["creds"]), Sessions: map[string]json.RawMessage{}, Now: func() time.Time { return time.UnixMilli(3333) }}
	for name, v := range nodeObj(nodeObj(b["values"])["session"]) {
		s.Sessions[name] = v
	}
	addr := sig.NewSignalAddress("111", 0)
	cipher := session.NewCipherFromSession(addr, s, nil, s, store.SignalProtobufSerializer.PreKeySignalMessage, store.SignalProtobufSerializer.SignalMessage)
	var packets []nodeObject
	_ = json.Unmarshal(source["packets"], &packets)
	data := nodeBytes(packets[4]["ciphertext"])
	data[len(data)-1] ^= 0x10
	packet, e := sig.NewSignalMessageFromBytes(data, store.SignalProtobufSerializer.SignalMessage)
	if e != nil {
		t.Fatal(e)
	}
	before := append([]byte{}, s.Sessions["111.0"]...)
	if _, e = s.DecryptNodeMessage(ctx, addr, cipher, packet); e == nil {
		t.Fatal("tampered MAC accepted")
	}
	if !bytes.Equal(before, s.Sessions["111.0"]) {
		t.Fatal("failed actual crypto mutated old record")
	}
}

func TestActualGoClosedOnlyLegacyRecordDecryptAndNoOpenOutbound(t *testing.T) {
	ctx := context.Background()
	source := actualNodePeer(t, map[string]any{"op": "setup"})
	b := nodeObj(source["b"])
	values := nodeObj(nodeObj(b["values"])["session"])
	v := nodeObj(values["111.0"])
	delete(v, "version")
	v["registrationId"] = nodeJSON(123)
	entries := nodeObj(v["_sessions"])
	for name, data := range entries {
		entry := nodeObj(data)
		delete(entry, "registrationId")
		index := nodeObj(entry["indexInfo"])
		index["closed"] = nodeJSON(4444)
		entry["indexInfo"] = nodeJSON(index)
		entries[name] = nodeJSON(entry)
	}
	v["_sessions"] = nodeJSON(entries)
	s := &NodeSignalStore{Creds: nodeObj(b["creds"]), Sessions: map[string]json.RawMessage{"111.0": nodeJSON(v)}, Now: func() time.Time { return time.UnixMilli(3333) }}
	addr := sig.NewSignalAddress("111", 0)
	cipher := session.NewCipherFromSession(addr, s, nil, s, store.SignalProtobufSerializer.PreKeySignalMessage, store.SignalProtobufSerializer.SignalMessage)
	if _, e := s.EncryptNodeMessage(ctx, addr, cipher, []byte("never")); e == nil || e.Error() != "No open session" {
		t.Fatal("closed outbound Source error", e)
	}
	// The production adapter applies the original registration inheritance;
	// decryption keeps closed state closed and does not reset prior keys.
	var packets []nodeObject
	_ = json.Unmarshal(source["packets"], &packets)
	m, e := sig.NewSignalMessageFromBytes(nodeBytes(packets[4]["ciphertext"]), store.SignalProtobufSerializer.SignalMessage)
	if e != nil {
		t.Fatal(e)
	}
	plain, e := s.DecryptNodeMessage(ctx, addr, cipher, m)
	if e != nil || string(plain) != "NODE_MESSAGE_4" {
		t.Fatal("closed incoming old state failed", e)
	}
	root := nodeObj(s.Sessions["111.0"])
	for _, entry := range nodeObj(root["_sessions"]) {
		if nodeNumber(nodeObj(nodeObj(entry)["indexInfo"])["closed"]) != 4444 {
			t.Fatal("closed original state reopened")
		}
	}
}
