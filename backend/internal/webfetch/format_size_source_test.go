package webfetch

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestActualFileSizeSourceBinary64DecimalTiesNeighborsAndUnitThresholds(t *testing.T) {
	sizes := []int{0, 1, 1023, 1024, 1025, 1279, 1280, 1281, 1536, 1048575, 1048576, 1048577, 1310719, 1310720, 1310721, 1073741823, 1073741824, 1073741825, 1342177279, 1342177280, 1342177281}
	fractions := []float64{1.15, 1.25, 1.35, 1.45, 1.55, 1.65, 1.75, math.Nextafter(1.25, 0), math.Nextafter(1.25, 2), math.Nextafter(1.15, 0), math.Nextafter(1.15, 2)}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual pinned Source Bun required")
	}
	home := t.TempDir()
	fixture, e := filepath.Abs("../../testdata/native-file-size-format.fixture.ts")
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(bun, fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + os.TempDir(), "CC_HAHA_FILE_SIZE_FORMAT_FIXTURE=1"}
	input, _ := json.Marshal(map[string]any{"sizes": sizes, "fractions": fractions})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	p, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var want struct{ Sizes, Fractions []string }
	if e = json.Unmarshal(p, &want); e != nil {
		t.Fatal(e)
	}
	actualSizes, actualFractions := []string{}, []string{}
	for _, size := range sizes {
		actualSizes = append(actualSizes, FormatSize(size))
	}
	for _, value := range fractions {
		actualFractions = append(actualFractions, formatSizeFixedOne(value)+"KB")
	}
	if !reflect.DeepEqual(actualSizes, want.Sizes) || !reflect.DeepEqual(actualFractions, want.Fractions) {
		t.Fatalf("actual Source binary64 file-size rounding differs\nGo%v %v\nSource%v %v", actualSizes, actualFractions, want.Sizes, want.Fractions)
	}
}
