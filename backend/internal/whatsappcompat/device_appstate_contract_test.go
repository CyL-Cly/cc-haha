package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"google.golang.org/protobuf/proto"
)

type appStateSourceProof struct {
	Directory, ID, Raw, KeyHex, Timestamp, FingerprintHex string
}

func appStateOriginalPersistence(t *testing.T, home, operation string) appStateSourceProof {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual original app-state producer requires Source Bun")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", nodeSourceFixturePath("node_appstate_persistence.fixture.ts"))
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	child.Stdin = bytes.NewReader(nodeJSON(map[string]string{"homeDir": home, "operation": operation}))
	var stderr bytes.Buffer
	child.Stderr = &stderr
	data, err := child.Output()
	if err != nil {
		t.Fatalf("original app-state persistence %v %.2500s", err, stderr.String())
	}
	var result appStateSourceProof
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestActualOriginalAppStateKeyColdMigrationAndVersionPreservesFutureFields(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	source := appStateOriginalPersistence(t, home, "seed")
	_, native, err := OpenNodeDevice(source.Directory, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := base64.StdEncoding.DecodeString(source.ID)
	key, err := native.GetAppStateSyncKey(ctx, id)
	if err != nil || key == nil || hex.EncodeToString(key.Data) != source.KeyHex || key.Timestamp != 1700000000123 || hex.EncodeToString(key.Fingerprint) != source.FingerprintHex {
		t.Fatalf("actual original persisted key %s decoded native=%+v error=%v", source.Raw, key, err)
	}
	all, err := native.GetAllAppStateSyncKeys(ctx)
	if err != nil || len(all) != 1 || all[0].Timestamp != key.Timestamp || !bytes.Equal(all[0].Data, key.Data) {
		t.Fatalf("cold key inventory %+v %v", all, err)
	}
	native.Signal.Creds["myAppStateKeyId"] = nodeBuffer(id)
	latest, err := native.GetLatestAppStateSyncKeyID(ctx)
	if err != nil || !bytes.Equal(latest, id) {
		t.Fatal("actual source latest key identity", latest, err)
	}
	version, hash, err := native.GetAppStateVersion(ctx, "regular")
	if err != nil || version != 8 || !bytes.Equal(hash[:], bytes.Repeat([]byte{7}, 128)) {
		t.Fatal("original version/hash migration", version, err)
	}
	if err = native.PutAppStateMutationMACs(ctx, "regular", 8, []store.AppStateMutationMAC{{IndexMAC: []byte("first"), ValueMAC: []byte("value-mac")}}); err != nil {
		t.Fatal(err)
	}
	nextHash := [128]byte{9, 8, 7}
	if err = native.PutAppStateVersion(ctx, "regular", 9, nextHash); err != nil {
		t.Fatal(err)
	}
	physical, err := os.ReadFile(native.keyPath("app-state-sync-version", "regular"))
	if err != nil || !bytes.Contains(physical, []byte(`"futureVersionField":{"untouched":true}`)) {
		t.Fatalf("original unknown version data lost %s %v", physical, err)
	}
	_, cold, err := OpenNodeDevice(source.Directory, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	version, hash, err = cold.GetAppStateVersion(ctx, "regular")
	if err != nil || version != 9 || hash != nextHash {
		t.Fatal("cold committed app-state version", version, hash, err)
	}
	mac, err := cold.GetAppStateMutationMAC(ctx, "regular", []byte("first"))
	if err != nil || string(mac) != "value-mac" {
		t.Fatal("cold committed mutation MAC", mac, err)
	}
	if err = cold.DeleteAppStateMutationMACs(ctx, "regular", [][]byte{[]byte("first")}); err != nil {
		t.Fatal(err)
	}
	mac, err = cold.GetAppStateMutationMAC(ctx, "regular", []byte("first"))
	if err != nil || len(mac) != 0 {
		t.Fatal("deleted mutation MAC retained", mac, err)
	}
	if err = cold.PutAppStateSyncKey(ctx, id, store.AppStateSyncKey{Data: []byte{2, 3, 4}, Timestamp: 1700000000456, Fingerprint: key.Fingerprint}); err != nil {
		t.Fatal(err)
	}
	// The actual original consumer must read the native replacement after restart.
	peer := appStateOriginalPersistence(t, home, "read")
	if peer.KeyHex != "020304" || peer.Timestamp != "1700000000456" || peer.FingerprintHex != source.FingerprintHex {
		t.Fatalf("native app-state could not cold-forward to original peer: %+v", peer)
	}
	if err = cold.DeleteAppStateVersion(ctx, "regular"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(cold.keyPath("app-state-sync-version", "regular")); !os.IsNotExist(err) {
		t.Fatal("deleted version file remains", err)
	}
}

func TestAppStateMalformedFingerprintAndVersionAreRejectedWithoutCommit(t *testing.T) {
	ctx := context.Background()
	_, native, err := OpenNodeDevice(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	id := []byte("physical-invalid-key")
	if err = native.PutAppStateSyncKey(ctx, id, store.AppStateSyncKey{Fingerprint: []byte{0x80}}); err == nil {
		t.Fatal("malformed wire fingerprint committed")
	}
	if _, err = os.Stat(native.keyPath("app-state-sync-key", base64.StdEncoding.EncodeToString(id))); !os.IsNotExist(err) {
		t.Fatal("invalid fingerprint touched physical key store", err)
	}
	fingerprint, err := proto.Marshal(&waE2E.AppStateSyncKeyFingerprint{RawID: proto.Uint32(3), DeviceIndexes: []uint32{1}})
	if err != nil {
		t.Fatal(err)
	}
	if err = native.PutAppStateSyncKey(ctx, id, store.AppStateSyncKey{Data: []byte("key"), Fingerprint: fingerprint, Timestamp: 123}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(native.keyPath("app-state-sync-key", base64.StdEncoding.EncodeToString(id)))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = native.PutAppStateSyncKey(canceled, id, store.AppStateSyncKey{Data: []byte("must-not-replace"), Fingerprint: fingerprint}); err == nil {
		t.Fatal("canceled key replacement committed")
	}
	after, _ := os.ReadFile(native.keyPath("app-state-sync-key", base64.StdEncoding.EncodeToString(id)))
	if !bytes.Equal(before, after) {
		t.Fatal("canceled replacement changed persisted key")
	}
	if err = native.putKey(ctx, "app-state-sync-version", "corrupt", nodeObject{"version": nodeJSON(1), "hash": nodeBuffer([]byte{1})}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = native.GetAppStateVersion(ctx, "corrupt"); err == nil {
		t.Fatal("corrupt prior 128-byte hash accepted")
	}
}

func TestNativeAuthAppStateKeyOldJSONRepresentationsUpgradeWithoutGlobalMarker(t *testing.T) {
	for _, fixture := range []struct {
		name, timestamp string
		want            int64
	}{
		{"original-protobufjs-string", `"1700000000123"`, 1700000000123},
		{"older-native-number", `1700000000123`, 1700000000123},
		{"signed-int64-without-float-rounding", `"9223372036854775807"`, 9223372036854775807},
		{"omitted-proto-timestamp", `null`, 0},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			ctx := context.Background()
			_, native, err := OpenNodeDevice(t.TempDir(), time.Now)
			if err != nil {
				t.Fatal(err)
			}
			id := []byte("old-native-id")
			path := native.keyPath("app-state-sync-key", base64.StdEncoding.EncodeToString(id))
			// This is the previous Go JSON spelling and numeric timestamp shape.
			legacy := []byte(`{"keyData":{"type":"Buffer","data":"AQID"},"fingerprint":{"rawID":0,"currentIndex":4,"deviceIndexes":[]},"timestamp":` + fixture.timestamp + `}`)
			if err = os.WriteFile(path, legacy, 0600); err != nil {
				t.Fatal(err)
			}
			key, err := native.GetAppStateSyncKey(ctx, id)
			if err != nil || key == nil || key.Timestamp != fixture.want || !bytes.Equal(key.Data, []byte{1, 2, 3}) {
				t.Fatalf("old native timestamp %s %+v %v", legacy, key, err)
			}
			var fingerprint waE2E.AppStateSyncKeyFingerprint
			if err = proto.Unmarshal(key.Fingerprint, &fingerprint); err != nil || fingerprint.RawID == nil || *fingerprint.RawID != 0 || fingerprint.GetCurrentIndex() != 4 {
				t.Fatal("old fingerprint presence lost", fingerprint.String(), err)
			}
			if err = native.PutAppStateSyncKey(ctx, id, *key); err != nil {
				t.Fatal(err)
			}
			upgraded, err := os.ReadFile(path)
			if err != nil || !bytes.Contains(upgraded, []byte(`"rawId":0`)) || bytes.Contains(upgraded, []byte(`"rawID"`)) || bytes.Contains(upgraded, []byte(`schemaVersion`)) {
				t.Fatalf("lazy original-compatible replacement %s %v", upgraded, err)
			}
		})
	}
	for _, timestamp := range []string{`"not-a-timestamp"`, `"9223372036854775808"`, `{}`, `true`} {
		if _, err := appStateTimestamp([]byte(timestamp)); err == nil {
			t.Fatalf("malformed persisted timestamp silently became zero: %s", timestamp)
		}
	}
}

func TestAppStateLookupCancellationAndCorruptPersistedValuesDoNotBecomeMissingKeys(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	_, native, err := OpenNodeDevice(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = native.GetAllAppStateSyncKeys(canceled); err != context.Canceled {
		t.Fatal("canceled empty-directory enumeration returned successful key inventory", err)
	}
	missing, err := native.GetAppStateSyncKey(ctx, []byte("unavailable peer key"))
	if err != nil || missing != nil {
		t.Fatal("ordinary absent app-state key is a missing receipt", missing, err)
	}
	id := []byte("corrupt persisted timestamp")
	value := nodeObject{"keyData": nodeBuffer([]byte{1}), "fingerprint": nodeJSON(map[string]any{"rawId": 7}), "timestamp": nodeJSON("not-an-int64")}
	if err = native.putKey(ctx, "app-state-sync-key", base64.StdEncoding.EncodeToString(id), value); err != nil {
		t.Fatal(err)
	}
	path := native.keyPath("app-state-sync-key", base64.StdEncoding.EncodeToString(id))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if key, err := native.GetAppStateSyncKey(ctx, id); err == nil || key != nil {
		t.Fatal("malformed persisted timestamp became a valid or missing key", key, err)
	}
	if _, err = native.GetAllAppStateSyncKeys(ctx); err == nil {
		t.Fatal("bulk enumeration ignored corrupt retained key")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("reading invalid auth state repaired protected bytes", err)
	}
	// A disconnected directory is an I/O failure, not a valid empty snapshot.
	native.Signal.Dir = filepath.Join(dir, "not-mounted")
	if _, err = native.GetAllAppStateSyncKeys(ctx); !os.IsNotExist(err) {
		t.Fatal("directory-loss became empty app-state snapshot", err)
	}
}
