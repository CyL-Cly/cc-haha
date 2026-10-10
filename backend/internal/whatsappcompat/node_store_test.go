package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/json"
	"go.mau.fi/whatsmeow/store"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestActualNodeSignalToNativeAndBackPreservesActiveArchivedSkippedKeys(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("native-whatsapp-signal.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	o := nodeObj(data)
	local := nodeObj(o["localPublic"])
	creds := nodeObject{"registrationId": nodeJSON(456), "signedIdentityKey": nodeJSON(nodeObject{"public": local["data"]})}
	s := NodeSignalStore{Creds: creds, Now: func() time.Time { return time.UnixMilli(3333) }}
	shape, e := s.importRecord(o["record"])
	if e != nil {
		t.Fatal(e)
	}
	native := store.SignalProtobufSerializer.Session.Serialize(shape)
	shape, e = store.SignalProtobufSerializer.Session.Deserialize(native)
	if e != nil {
		t.Fatal(e)
	}
	got, e := s.exportRecord(shape, o["record"])
	if e != nil {
		t.Fatal(e)
	}
	var a, b any
	_ = json.Unmarshal(got, &a)
	_ = json.Unmarshal(o["record"], &b)
	if !bytes.Equal(nodeJSON(a), nodeJSON(b)) {
		t.Fatalf("lossy candidate roundtrip\nGo%s\nSource%s", got, o["record"])
	}
}
