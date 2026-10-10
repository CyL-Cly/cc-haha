package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"go.mau.fi/libsignal/kdf"
	"go.mau.fi/libsignal/keys/message"
	"go.mau.fi/whatsmeow/store"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestActualNodeSignalRecordIsNotDefaultGoSessionSchemaAndSeedNeedsRetention(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("original Source Bun required")
	}
	fixture := nodeSourceFixturePath("native-whatsapp-signal.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source %v %s", e, stderr.String())
	}
	var source struct {
		Record, Roundtrip json.RawMessage
		MessageSeed       struct{ Type, Data string }
		Derived           struct{ CipherKey, MacKey, IV struct{ Type, Data string } }
	}
	if e = json.Unmarshal(data, &source); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(source.Record, source.Roundtrip) {
		t.Fatal("original Source self roundtrip changed")
	}
	native, e := store.SignalProtobufSerializer.Session.Deserialize(source.Record)
	if e != nil {
		t.Fatal(e)
	}
	if native.SessionState != nil || len(native.PreviousStates) != 0 {
		t.Fatal("default Go serializer unexpectedly knows original Node schema")
	}
	seed, e := base64.StdEncoding.DecodeString(source.MessageSeed.Data)
	if e != nil {
		t.Fatal(e)
	}
	expanded, e := kdf.DeriveSecrets(seed, nil, []byte(message.KdfSalt), 80)
	if e != nil {
		t.Fatal(e)
	}
	for i, want := range []string{source.Derived.CipherKey.Data, source.Derived.MacKey.Data, source.Derived.IV.Data} {
		start, end := 0, 32
		if i == 1 {
			start, end = 32, 64
		} else if i == 2 {
			start, end = 64, 80
		}
		if base64.StdEncoding.EncodeToString(expanded[start:end]) != want {
			t.Fatal("actual Source→Go skipped-key forward derivation mismatch", i)
		}
	}
	keys := message.KeysStructure{CipherKey: expanded[:32], MacKey: expanded[32:64], IV: expanded[64:80], Index: 3}
	serialized, e := json.Marshal(keys)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(serialized, []byte(source.MessageSeed.Data)) {
		t.Fatal("Go structure unexpectedly retains original seed")
	}
	t.Log("actual Source v1 active+archived records roundtrip; default Go Session schema loses both; skipped-key derive matches but native KeysStructure omits source seed, requiring prior-state/seed reconstruction adapter")
}
