package imagegeneration

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

func TestImagePublicAddressGuardAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	addresses := []string{"", "hostname", "127.0.0.1", "127.20.0.1", "0.0.0.0", "10.0.0.1", "169.254.169.254", "172.16.1.1", "172.31.255.255", "172.32.0.1", "192.168.1.1", "192.0.0.1", "192.0.2.1", "192.0.1.1", "198.18.1.1", "198.19.1.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "255.255.255.255", "8.8.8.8", "1.1.1.1", "100.64.0.1", "::", "::1", "::ffff:8.8.8.8", "::ffff:127.0.0.1", "fe80::1", "fc00::1", "2002:1234::1", "2001::1", "2001:0:1::1", "2001:db8::1", "2001:4860:4860::8888", "2606:4700:4700::1111", "4000::1", "3000::1", "2001:0000:0000:0000:0000:0000:0000:0001", "fe80::1%en0"}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-image-download-address.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_IMAGE_DOWNLOAD_ADDRESS_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(addresses)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var expected []string
	if err = json.Unmarshal(raw, &expected); err != nil || len(expected) != len(addresses) {
		t.Fatal(err, string(raw))
	}
	for i, address := range addresses {
		value := ""
		if err := AssertPublicAddress(address); err != nil {
			value = err.Error()
		}
		if value != expected[i] {
			t.Errorf("address %q Go%q Source%q", address, value, expected[i])
		}
	}
}
