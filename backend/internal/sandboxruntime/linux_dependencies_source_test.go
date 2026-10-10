package sandboxruntime

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestActualSourceLinuxDependencyErrorsWarningsExactPresenceMatrix(t *testing.T) {
	rows := []LinuxDependencyStatus{}
	for i := 0; i < 16; i++ {
		rows = append(rows, LinuxDependencyStatus{HasBwrap: i&1 != 0, HasSocat: i&2 != 0, HasSeccompBPF: i&4 != 0, HasSeccompApply: i&8 != 0})
	}
	input, _ := json.Marshal(map[string]any{"rows": rows})
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/linux-dependencies.fixture.ts")
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(input)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(output))
	}
	var source []DependencyCheck
	if e = json.Unmarshal(output, &source); e != nil {
		t.Fatal(e, string(output))
	}
	for i, row := range rows {
		actual := CheckLinuxDependencies(row)
		if !reflect.DeepEqual(actual, source[i]) {
			t.Fatal(i, source[i], actual)
		}
	}
}
