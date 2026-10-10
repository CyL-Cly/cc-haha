package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeNewCredentialsAreVerifiedByActualOriginalSource(t *testing.T) {
	creds, e := CreateNodeCredentials()
	if e != nil {
		t.Fatal(e)
	}
	device, e := DeviceFromNodeCredentials(creds)
	if e != nil || device.ID != nil || device.NoiseKey == nil || device.IdentityKey == nil {
		t.Fatal("real new credential device", e)
	}
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("node_device.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(nodeJSON(map[string]any{"creds": creds}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal("Source", e, stderr.String())
	}
	var proof map[string]any
	if e = json.Unmarshal(out, &proof); e != nil {
		t.Fatal(e)
	}
	for _, field := range []string{"sameFields", "validSignedPrekey", "registrationInSourceRange"} {
		if proof[field] != true {
			t.Fatal("original credential mismatch", field, string(out))
		}
	}
	t.Log("actual original Source verified native Curve signature, original fields/defaults and registration range")
}
