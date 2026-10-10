package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sig "go.mau.fi/libsignal/protocol"
	"go.mau.fi/libsignal/session"
	"go.mau.fi/libsignal/signalerror"
	"go.mau.fi/whatsmeow/store"
)

func originalForkEdges(t *testing.T, input any) nodeObject {
	t.Helper()
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("node_fork_edges.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(nodeJSON(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal("actual original fork edge", err, stderr.String())
	}
	return nodeObj(output)
}

func forkDeviceFromOriginal(t *testing.T, b nodeObject) (*store.Device, *NodeDeviceStore, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "creds.json"), b["creds"], 0600); err != nil {
		t.Fatal(err)
	}
	for name, raw := range nodeObj(nodeObj(b["values"])["session"]) {
		if err := os.WriteFile(filepath.Join(dir, "session-"+name+".json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	device, n, err := OpenNodeDevice(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return device, n, dir
}

func TestForkActualOriginalFirstPreKeyCipherCancelCommitAndColdRestore(t *testing.T) {
	source := originalForkEdges(t, map[string]any{"op": "prekey"})
	packet := nodeObj(source["packet"])
	if nodeString(packet["type"]) != "pkmsg" {
		t.Fatal("original first prekey packet absent")
	}
	raw := nodeBytes(packet["ciphertext"])
	proof := originalForkEdges(t, map[string]any{"op": "decrypt", "b": source["b"], "type": "pkmsg", "ciphertext": base64.StdEncoding.EncodeToString(raw)})
	if nodeString(proof["plain"]) != "PRIVATE_FIRST_PREKEY" {
		t.Fatal("actual original first PreKey did not succeed", nodeString(proof["error"]))
	}
	prekey, err := sig.NewPreKeySignalMessageFromBytes(raw, store.SignalProtobufSerializer.PreKeySignalMessage, store.SignalProtobufSerializer.SignalMessage)
	if err != nil {
		t.Fatal(err)
	}
	device, n, dir := forkDeviceFromOriginal(t, nodeObj(source["b"]))
	addr := sig.NewSignalAddress("111", 0)
	cipher := session.NewCipher(session.NewBuilderFromSignal(device, addr, store.SignalProtobufSerializer), addr)
	ctx := session.WithNodeSessionTransactions(context.Background(), n.AcquireSessions)
	_, release, err := n.AcquireSessions(ctx, []string{"111:0"})
	if err != nil {
		t.Fatal(err)
	}
	held := true
	defer func() {
		if held {
			release()
		}
	}()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = cipher.Encrypt(canceled, []byte("MUST_NOT_ENCRYPT")); !errors.Is(err, context.Canceled) {
		t.Fatal("Encrypt cancellation", err)
	}
	if _, err = cipher.Decrypt(canceled, prekey.WhisperMessage()); !errors.Is(err, context.Canceled) {
		t.Fatal("Decrypt cancellation", err)
	}
	if _, err = cipher.DecryptMessage(canceled, prekey); !errors.Is(err, context.Canceled) {
		t.Fatal("PreKey cancellation", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "session-111.0.json")); !os.IsNotExist(err) {
		t.Fatal("canceled first packet wrote session", err)
	}
	release()
	held = false
	plain, err := cipher.DecryptMessage(ctx, prekey)
	if err != nil || string(plain) != "PRIVATE_FIRST_PREKEY" {
		t.Fatal("actual first PreKey decrypt", string(plain), err)
	}
	persisted, err := os.ReadFile(filepath.Join(dir, "session-111.0.json"))
	if err != nil {
		t.Fatal(err)
	}
	if nodeString(proof["plain"]) != string(plain) {
		t.Fatal("original first PreKey plaintext mismatch")
	}
	cold, coldStore, err := OpenNodeDevice(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	coldCipher := session.NewCipher(session.NewBuilderFromSignal(cold, addr, store.SignalProtobufSerializer), addr)
	coldCtx := session.WithNodeSessionTransactions(context.Background(), coldStore.AcquireSessions)
	if _, err = coldCipher.DecryptMessage(coldCtx, prekey); err == nil {
		t.Fatal("cold replay authenticated twice")
	}
	proof = originalForkEdges(t, map[string]any{"op": "decrypt", "b": proof["b"], "type": "pkmsg", "ciphertext": base64.StdEncoding.EncodeToString(raw)})
	if nodeString(proof["error"]) == "" || !bytes.Equal(proof["unchanged"], []byte("true")) {
		t.Fatal("original replay boundary", nodeString(proof["error"]))
	}
	after, err := os.ReadFile(filepath.Join(dir, "session-111.0.json"))
	if err != nil || !bytes.Equal(after, persisted) {
		t.Fatal("cold replay modified physical session", err)
	}
	// Replay failure must release its transaction for the next outbound operation.
	reply, err := coldCipher.Encrypt(coldCtx, []byte("PRIVATE_AFTER_REPLAY"))
	if err != nil {
		t.Fatal("failed PreKey transaction retained address", err)
	}
	sourceReply := actualNodePeer(t, map[string]any{"op": "continue", "round": 1, "a": source["a"], "b": source["b"], "replies": []map[string]string{{"type": "msg", "ciphertext": base64.StdEncoding.EncodeToString(reply.Serialize())}}})
	var replies []string
	if err = json.Unmarshal(sourceReply["plain"], &replies); err != nil || len(replies) != 1 || replies[0] != "PRIVATE_AFTER_REPLAY" {
		t.Fatal("actual original peer did not consume cold reply", replies, err)
	}
}

func TestForkActualDeviceBadMACAndCanceledRecordLeavePhysicalSessionUntouched(t *testing.T) {
	source := actualNodePeer(t, map[string]any{"op": "setup"})
	b := nodeObj(source["b"])
	device, n, dir := forkDeviceFromOriginal(t, b)
	var packets []nodeObject
	if err := json.Unmarshal(source["packets"], &packets); err != nil {
		t.Fatal(err)
	}
	raw := nodeBytes(packets[4]["ciphertext"])
	raw[len(raw)-1] ^= 0x10
	packet, err := sig.NewSignalMessageFromBytes(raw, store.SignalProtobufSerializer.SignalMessage)
	if err != nil {
		t.Fatal(err)
	}
	addr := sig.NewSignalAddress("111", 0)
	cipher := session.NewCipher(session.NewBuilderFromSignal(device, addr, store.SignalProtobufSerializer), addr)
	ctx := session.WithNodeSessionTransactions(context.Background(), n.AcquireSessions)
	before, err := os.ReadFile(filepath.Join(dir, "session-111.0.json"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := device.LoadSession(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	recordBefore := record.Serialize()
	canceled, cancel := context.WithCancel(session.WithNodeSessionSemantics(context.Background()))
	cancel()
	if _, _, err = cipher.DecryptWithRecord(canceled, record, packet); !errors.Is(err, context.Canceled) {
		t.Fatal("record cancellation", err)
	}
	if !bytes.Equal(record.Serialize(), recordBefore) {
		t.Fatal("canceled record changed")
	}
	if _, err = cipher.Decrypt(ctx, packet); !errors.Is(err, signalerror.ErrNoValidSessions) {
		t.Fatal("bad MAC candidates", err)
	}
	proof := originalForkEdges(t, map[string]any{"op": "decrypt", "b": b, "type": "msg", "ciphertext": base64.StdEncoding.EncodeToString(raw)})
	if nodeString(proof["error"]) != "No matching sessions found for message" || !bytes.Equal(proof["unchanged"], []byte("true")) {
		t.Fatal("actual original bad MAC boundary", nodeString(proof["error"]))
	}
	after, err := os.ReadFile(filepath.Join(dir, "session-111.0.json"))
	if err != nil || !bytes.Equal(after, before) {
		t.Fatal("bad MAC changed physical old record", err)
	}
	valid, err := sig.NewSignalMessageFromBytes(nodeBytes(packets[4]["ciphertext"]), store.SignalProtobufSerializer.SignalMessage)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := cipher.Decrypt(ctx, valid)
	if err != nil || string(plain) != "NODE_MESSAGE_4" {
		t.Fatal("bad MAC poisoned subsequent genuine packet", string(plain), err)
	}
}

func TestForkActualDevicePrefetchFileFailureReleasesAddressForColdRepair(t *testing.T) {
	device, n, dir := forkDeviceFromOriginal(t, nodeObj(actualNodePeer(t, map[string]any{"op": "setup"})["b"]))
	path := filepath.Join(dir, "session-111.0.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := nodeObj(original)
	entries := nodeObj(corrupt["_sessions"])
	for id, raw := range entries {
		state := nodeObj(raw)
		index := nodeObj(state["indexInfo"])
		index["remoteIdentityKey"] = nodeEncoded([]byte{0})
		state["indexInfo"] = nodeJSON(index)
		entries[id] = nodeJSON(state)
	}
	corrupt["_sessions"] = nodeJSON(entries)
	if err = os.WriteFile(path, nodeJSON(corrupt), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := session.WithNodeSessionTransactions(context.Background(), n.AcquireSessions)
	if _, _, err = device.WithCachedSessions(ctx, []string{"111:0"}); err == nil || !strings.Contains(err.Error(), "failed to prefetch sessions:") {
		t.Fatal("physical invalid-key prefetch failure", err)
	}
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	// Source caches reads for five minutes. Restore the same physical file via
	// the existing cache Set producer, rather than pretending external edits
	// invalidate a warm key-cache snapshot.
	if err = n.cache.Set(ctx, map[string]map[string]any{"session": {"111.0": json.RawMessage(original)}}); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	existing, cached, err := device.WithCachedSessions(deadline, []string{"111:0"})
	if err != nil || !existing["111:0"] {
		t.Fatal("prefetch failure kept gate after actual file repair", existing, err)
	}
	if err = device.PutCachedSessions(cached); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("clean repaired prefetch changed original physical record", err)
	}
}
