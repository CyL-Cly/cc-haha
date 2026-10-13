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
	"testing"
	"time"

	"go.mau.fi/libsignal/groups"
	groupRecord "go.mau.fi/libsignal/groups/state/record"
	"go.mau.fi/libsignal/protocol"
	"go.mau.fi/whatsmeow/store"
)

func originalGroupCipher(t *testing.T, home string, input map[string]any) nodeObject {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual original group cipher requires Source Bun")
	}
	input["homeDir"] = home
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", nodeSourceFixturePath("node_group_cipher.fixture.ts"))
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	child.Stdin = bytes.NewReader(nodeJSON(input))
	var stderr bytes.Buffer
	child.Stderr = &stderr
	output, err := child.Output()
	if err != nil {
		t.Fatal("actual original group crypto", err, stderr.String())
	}
	if !json.Valid(output) {
		t.Fatal("original group crypto did not return a receipt", string(output))
	}
	return nodeObj(output)
}

func coldGroupCipher(t *testing.T, directory, group string) (*NodeDeviceStore, *groups.GroupCipher) {
	t.Helper()
	device, native, err := OpenNodeDevice(directory, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	name := protocol.NewSenderKeyName(group, protocol.NewSignalAddress("111", 0))
	builder := groups.NewGroupSessionBuilder(device, store.SignalProtobufSerializer)
	return native, groups.NewGroupCipher(builder, name, device)
}

func decodeGroupPacket(t *testing.T, encoded string) *protocol.SenderKeyMessage {
	t.Helper()
	wire, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := protocol.NewSenderKeyMessageFromBytes(wire, store.SignalProtobufSerializer.SenderKeyMessage)
	if err != nil {
		t.Fatal("actual original sender-key packet rejected", err)
	}
	return packet
}

func TestActualOriginalSenderKeyColdAuthOutOfOrderNativeForwardAndReplay(t *testing.T) {
	home := t.TempDir()
	source := originalGroupCipher(t, home, map[string]any{"operation": "seed"})
	group := nodeString(source["group"])
	receiverDir, senderDir := nodeString(source["receiverDir"]), nodeString(source["senderDir"])
	path := filepath.Join(receiverDir, nodeString(source["keyFile"]))
	var packets []string
	if err := json.Unmarshal(source["packets"], &packets); err != nil || len(packets) != 7 {
		t.Fatal("actual sender packet inventory", packets, err)
	}
	// Original persistent sender keys are Buffer-wrapped JSON records. The
	// native Device/SenderKey/GroupCipher consumers must use these exact files.
	outer := nodeObj([]byte(nodeString(source["receiverRaw"])))
	var originalRows []nodeObject
	if json.Unmarshal(nodeBytes(nodeJSON(outer)), &originalRows) != nil || len(originalRows) != 1 {
		t.Fatal("actual original receiver record shape", string(source["receiverRaw"]))
	}
	originalRows[0]["futureStateField"] = nodeJSON(map[string]any{"keep": true})
	if err := os.WriteFile(path, nodeJSON(nodeBuffer(nodeJSON(originalRows))), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, receiver := coldGroupCipher(t, receiverDir, group)
	plain, err := receiver.Decrypt(ctx, decodeGroupPacket(t, packets[4]))
	if err != nil || string(plain) != "ACTUAL_GROUP_PACKET_4" {
		t.Fatal("actual original out-of-order group decrypt", string(plain), err)
	}
	_, receiver = coldGroupCipher(t, receiverDir, group)
	plain, err = receiver.Decrypt(ctx, decodeGroupPacket(t, packets[1]))
	if err != nil || string(plain) != "ACTUAL_GROUP_PACKET_1" {
		t.Fatal("native cold skipped sender seed lost", string(plain), err)
	}
	proof := originalGroupCipher(t, home, map[string]any{"operation": "decrypt", "packet": packets[2]})
	if nodeString(proof["plain"]) != "ACTUAL_GROUP_PACKET_2" || string(proof["error"]) != "null" {
		t.Fatal("actual original consumer rejected native skipped seed", string(nodeJSON(proof)))
	}
	_, sender := coldGroupCipher(t, senderDir, group)
	forward, err := sender.Encrypt(ctx, []byte("NATIVE_COLD_GROUP_PACKET_7"))
	if err != nil {
		t.Fatal("original outgoing signing key did not cold-migrate", err)
	}
	proof = originalGroupCipher(t, home, map[string]any{"operation": "decrypt", "packet": base64.StdEncoding.EncodeToString(forward.SignedSerialize())})
	if nodeString(proof["plain"]) != "NATIVE_COLD_GROUP_PACKET_7" {
		t.Fatal("actual original group peer rejected native signed packet", string(nodeJSON(proof)))
	}
	_, receiver = coldGroupCipher(t, receiverDir, group)
	bad, _ := base64.StdEncoding.DecodeString(packets[5])
	bad[len(bad)-1] ^= 1
	before, _ := os.ReadFile(path)
	if _, err = receiver.Decrypt(ctx, decodeGroupPacket(t, base64.StdEncoding.EncodeToString(bad))); err == nil {
		t.Fatal("invalid original group signature authenticated")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("bad group signature committed a ratchet")
	}
	plain, err = receiver.Decrypt(ctx, decodeGroupPacket(t, packets[5]))
	if err != nil || string(plain) != "ACTUAL_GROUP_PACKET_5" {
		t.Fatal("tamper failure lost retained genuine skipped key", string(plain), err)
	}
	proof = originalGroupCipher(t, home, map[string]any{"operation": "decrypt", "packet": packets[5]})
	if string(proof["error"]) == "null" || string(proof["unchanged"]) != "true" {
		t.Fatal("native replay state was not consumed by actual original peer", string(nodeJSON(proof)))
	}
	proof = originalGroupCipher(t, home, map[string]any{"operation": "encrypt", "plain": "ORIGINAL_AFTER_NATIVE_GROUP_UPDATE"})
	_, receiver = coldGroupCipher(t, receiverDir, group)
	plain, err = receiver.Decrypt(ctx, decodeGroupPacket(t, nodeString(proof["packet"])))
	if err != nil || string(plain) != "ORIGINAL_AFTER_NATIVE_GROUP_UPDATE" {
		t.Fatal("original sender could not continue from native auth update", string(plain), err)
	}
	_, receiver = coldGroupCipher(t, receiverDir, group)
	plain, err = receiver.Decrypt(ctx, decodeGroupPacket(t, packets[6]))
	if err != nil || string(plain) != "ACTUAL_GROUP_PACKET_6" {
		t.Fatal("second cold hop discarded delayed sender-key packet", string(plain), err)
	}
	final, _ := os.ReadFile(path)
	var rows []nodeObject
	if json.Unmarshal(nodeBytes(final), &rows) != nil || len(rows) != 1 || string(rows[0]["futureStateField"]) != `{"keep":true}` || len(nodeBytes(nodeObj(rows[0]["senderSigningKey"])["private"])) != 0 {
		t.Fatal("native sender-key commit lost opaque state or introduced recipient private key", string(final))
	}
	_, sender = coldGroupCipher(t, senderDir, group)
	senderPath := filepath.Join(senderDir, nodeString(source["keyFile"]))
	before, _ = os.ReadFile(senderPath)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = sender.Encrypt(canceled, []byte("MUST_NOT_COMMIT")); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled native group ratchet admitted", err)
	}
	after, _ = os.ReadFile(senderPath)
	if !bytes.Equal(before, after) {
		t.Fatal("canceled group operation changed physical sender key")
	}
	// A repeated distribution keeps an older duplicate ID in Node's record.
	// Native decryption must select the same oldest matching state, rather than
	// authenticating a previously consumed packet against the newly added key.
	originalGroupCipher(t, home, map[string]any{"operation": "redistribute", "distribution": nodeString(source["distribution"])})
	device, native, err := OpenNodeDevice(receiverDir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	name := protocol.NewSenderKeyName(group, protocol.NewSignalAddress("111", 0))
	builder := groups.NewGroupSessionBuilder(device, store.SignalProtobufSerializer)
	wire, _ := base64.StdEncoding.DecodeString(nodeString(source["distribution"]))
	distribution, err := protocol.NewSenderKeyDistributionMessageFromBytes(wire, store.SignalProtobufSerializer.SenderKeyDistributionMessage)
	if err != nil || builder.Process(ctx, name, distribution) != nil {
		t.Fatal("actual repeated distribution did not commit", err)
	}
	_, receiver = coldGroupCipher(t, receiverDir, group)
	before, _ = os.ReadFile(path)
	if _, err = receiver.Decrypt(ctx, decodeGroupPacket(t, packets[5])); err == nil {
		t.Fatal("new duplicate distribution bypassed original replay boundary")
	}
	after, _ = os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("rejected duplicate-ID replay changed original record")
	}
	proof = originalGroupCipher(t, home, map[string]any{"operation": "decrypt", "packet": packets[5]})
	if string(proof["error"]) == "null" || string(proof["unchanged"]) != "true" {
		t.Fatal("original duplicate-ID replay boundary differs", string(nodeJSON(proof)))
	}
	for index := 0; index < 5; index++ {
		device, native, err = OpenNodeDevice(receiverDir, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		builder = groups.NewGroupSessionBuilder(device, store.SignalProtobufSerializer)
		if err = builder.Process(ctx, name, distribution); err != nil {
			t.Fatal(err)
		}
	}
	final, _ = os.ReadFile(path)
	if json.Unmarshal(nodeBytes(final), &rows) != nil || len(rows) != 5 {
		t.Fatal("native distribution lost Source's five-state retirement boundary", len(rows))
	}
	if _, err = native.GetSenderKey(ctx, group, "111:0"); err != nil {
		t.Fatal("cold multi-state record did not deserialize", err)
	}
	// New native sender-key creation must still use the original auth envelope
	// and produce a distribution/signature that the original consumer accepts.
	if err = os.Remove(senderPath); err != nil {
		t.Fatal(err)
	}
	device, native, err = OpenNodeDevice(senderDir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	builder = groups.NewGroupSessionBuilder(device, store.SignalProtobufSerializer)
	created, err := builder.Create(ctx, name)
	if err != nil {
		t.Fatal("actual native empty-store key creation", err)
	}
	sender = groups.NewGroupCipher(builder, name, device)
	forward, err = sender.Encrypt(ctx, []byte("NATIVE_FRESH_GROUP_SESSION"))
	if err != nil {
		t.Fatal(err)
	}
	proof = originalGroupCipher(t, home, map[string]any{"operation": "decrypt-with-distribution", "distribution": base64.StdEncoding.EncodeToString(created.Serialize()), "packet": base64.StdEncoding.EncodeToString(forward.SignedSerialize())})
	if nodeString(proof["plain"]) != "NATIVE_FRESH_GROUP_SESSION" {
		t.Fatal("original peer rejected new native group key/distribution", string(nodeJSON(proof)))
	}
}

func TestSenderKeyInvalidRecordsAndCanceledCommitLeaveProtectedAuthUntouched(t *testing.T) {
	ctx := context.Background()
	_, native, err := OpenNodeDevice(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	group, user := "123-456@g.us", "111:0"
	id, err := nodeSenderKeyID(group, user)
	if err != nil || id != "123-456@g.us::111::0" {
		t.Fatal("actual original key category identity", id, err)
	}
	if value, err := native.GetSenderKey(ctx, group, "bad-address"); err == nil || value != nil {
		t.Fatal("invalid Signal address escaped sender-key boundary", value, err)
	}
	if err = native.PutSenderKey(ctx, group, "111:not-device", []byte(`{}`)); err == nil {
		t.Fatal("invalid device number created an auth key")
	}
	if err = native.PutSenderKey(ctx, group, user, []byte(`not-json`)); err == nil {
		t.Fatal("invalid native serialization committed")
	}
	for _, value := range []json.RawMessage{nodeBuffer([]byte(`null`)), nodeBuffer([]byte(`{"not":"states"}`)), nodeBuffer([]byte(`[{"senderKeyId":3,"senderChainKey":{"iteration":0},"senderSigningKey":{}}]`)), nodeBuffer([]byte(`[{"senderKeyId":"bad-counter"}]`))} {
		if err = native.putKey(ctx, "sender-key", id, value); err != nil {
			t.Fatal(err)
		}
		path := native.keyPath("sender-key", id)
		before, _ := os.ReadFile(path)
		if _, err = native.GetSenderKey(ctx, group, user); err == nil {
			t.Fatal("malformed old sender-key record became an empty/new session", string(value))
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatal("reading invalid sender-key auth repaired protected data")
		}
	}
	if err = native.putKey(ctx, "sender-key", id, nil); err != nil {
		t.Fatal(err)
	}
	empty := store.SignalProtobufSerializer.SenderKeyRecord.Serialize(&groupRecord.SenderKeyStructure{})
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = native.PutSenderKey(canceled, group, user, empty); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled sender-key creation committed", err)
	}
	if _, err = os.Stat(native.keyPath("sender-key", id)); !os.IsNotExist(err) {
		t.Fatal("canceled sender key created a protected file", err)
	}
}
