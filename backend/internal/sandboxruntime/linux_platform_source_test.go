package sandboxruntime

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestActualSourceLinuxWSLPlatformSupportMatrix(t *testing.T) {
	values := []any{nil, "", "Linux 6.1 ordinary", "4.4.0-19041-Microsoft", "Linux Microsoft WSL2", "wsl1", "WSL3 Microsoft", "WSL01", "WSL10", "WSL0", "xWSL1x", "WSL2 WSL1", "WSL１ Microsoft"}
	input, _ := json.Marshal(map[string]any{"versions": values})
	fixture, _ := filepath.Abs("testdata/linux-platform.fixture.ts")
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(input)
	raw, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(raw))
	}
	var source []struct {
		Supported bool
		Platform  string
	}
	if e = json.Unmarshal(raw, &source); e != nil {
		t.Fatal(e, string(raw))
	}
	for i, v := range values {
		s, _ := v.(string)
		if got := LinuxCLIPlatform(s); got != source[i].Platform {
			t.Fatalf("Source%d CLI label expected%q got%q", i, source[i].Platform, got)
		}
		if got := LinuxSupported(s); got != source[i].Supported {
			t.Fatalf("Source%d %q expected%v got%v", i, s, source[i].Supported, got)
		}
	}
}
