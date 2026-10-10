package sandboxruntime

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestActualSourceSandboxAvailabilityFailIfUnavailableAndPassthroughPlatforms(t *testing.T) {
	rows := []map[string]any{}
	inputs := []AvailabilityInput{}
	for _, platform := range []string{"macos", "linux", "wsl", "windows"} {
		for _, enabled := range []bool{false, true} {
			for _, required := range []bool{false, true} {
				for _, support := range []bool{false, true} {
					for _, errors := range [][]string{{}, {"ripgrep (rg) not found"}, {"bubblewrap (bwrap) not installed", "socat not installed"}} {
						for _, list := range []string{"absent", "[]", `["macos"]`, `["linux","wsl"]`, `null`, `42`, `true`, `"macos"`, `""`, `{"length":0}`, `{"length":1}`} {
							settings := testObject(t, "{}")
							raw, _ := json.Marshal(map[string]any{"sandbox": map[string]any{"enabled": enabled, "failIfUnavailable": required}})
							settings = testObject(t, string(raw))
							initial := settings.Clone()
							sandbox, _ := initial.Object("sandbox")
							if list != "absent" {
								sandbox.Set("enabledPlatforms", json.RawMessage(list))
								b, _ := sandbox.MarshalJSON()
								initial.Set("sandbox", b)
							}
							rows = append(rows, map[string]any{"settings": settings, "initial": initial, "platform": platform, "supported": support, "errors": errors})
							inputs = append(inputs, AvailabilityInput{Settings: settings, InitialSettings: initial, Platform: platform, Supported: support, DependencyErrors: errors})
						}
					}
				}
			}
		}
	}
	input, _ := json.Marshal(map[string]any{"rows": rows})
	fixture, _ := filepath.Abs("testdata/availability.fixture.ts")
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source []Availability
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	for i, in := range inputs {
		actual := ResolveAvailability(in)
		if actual != source[i] {
			t.Fatalf("Source%d %+v expected%+v actual%+v", i, in, source[i], actual)
		}
	}
}
