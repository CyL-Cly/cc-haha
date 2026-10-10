package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActualSourcePRNumberRoundingOverflowAndRecognition(t *testing.T) {
	values := []string{"plain", "#0", "#000000", "#9007199254740991", "#9007199254740992", "#9007199254740993", "#9007199254740995", "#9223372036854775807", "#18446744073709551615", "#100000000000000000000", "#1000000000000000000000", "#" + strings.Repeat("9", 309), "https://ghe.invalid/o/r/pull/9007199254740993/?x=1#z", " #1", "#-1", "#١٢", "#123\n", "#123\r", "#123\r\n", "#123\u2028", "https://example.invalid/o/r/pull/123?x\ry", "https://example.invalid/o/r/pull/123\n"}
	for n := 1; n <= 308; n++ {
		values = append(values, "#1"+strings.Repeat("0", n))
	}
	input, _ := json.Marshal(map[string]any{"values": values})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("testdata/pr-number.fixture.ts")
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = []string{"HOME=" + t.TempDir(), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source []struct {
		Input  string
		Number *string
		Truthy bool
	}
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	for i, row := range source {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			n, e := ParsePRReference(row.Input)
			if e != nil {
				t.Fatal("actual Source accepts this PR Number", e)
			}
			if n == nil {
				if row.Number != nil {
					t.Fatal("Source number missing", *row.Number)
				}
				return
			}
			got := PRNumberString(*n)
			if row.Number == nil || got != *row.Number || (*n != 0) != row.Truthy {
				t.Fatalf("Source %v truthy=%v; Go %s", row.Number, row.Truthy, got)
			}
		})
	}
}
