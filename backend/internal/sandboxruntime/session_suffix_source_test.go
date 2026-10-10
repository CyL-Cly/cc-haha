package sandboxruntime

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestActualSourceSandboxSessionSuffixOriginalRadixAndNineDigitSlice(t *testing.T) {
	rows := []float64{0, .25, .5, .75, math.SmallestNonzeroFloat64, math.Nextafter(1, 0), .12345678901234567, .99999999999}
	input, _ := json.Marshal(map[string]any{"rows": rows})
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/session-suffix.fixture.ts")
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(input)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(output))
	}
	var source []string
	if e = json.Unmarshal(output, &source); e != nil {
		t.Fatal(e, string(output))
	}
	for i, value := range rows {
		got, e := SessionSuffix(value)
		if e != nil || got != source[i] {
			t.Fatalf("Source random%.18g suffix %q; native %q %v", value, source[i], got, e)
		}
	}
}
