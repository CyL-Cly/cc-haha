package queryhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActualSourceAsyncTaskOutputSpillTailUTF16AndPhysicalBytes(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	home := t.TempDir()
	rows := [][]map[string]any{
		{{"text": "first plain\n"}, {"stderr": true, "text": "stderr 🐋\n"}},
		{{"spill": true}, {"text": "single JSON first line omitted by original tail\n"}},
		{{"text": "initial stdout\n"}, {"stderr": true, "text": "initial stderr\n"}, {"spill": true}, {"text": "\none\ntwo\nthree\nfour\nfive\nsix\n"}, {"stderr": true, "text": "\nerror tail\n"}},
		{{"spill": true}, {"text": "\n{\"systemMessage\":\"actual-json\"}\n"}, {"text": "fragment without delimiter"}},
		{{"spill": true}, {"text": "\n" + strings.Repeat("🐋", 2050) + "\nshort\n"}, {"text": "\nUTF16🐋\n"}},
		{{"text": strings.Repeat("A", hookMemoryLimit-1)}, {"stderr": true, "text": "boundary🐋"}},
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-async-hook-output.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CLAUDE_CODE_TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_ASYNC_HOOK_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(commandJSON(map[string]any{"rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows []struct {
			Stdout, Stderr, Path string
			Disk                 *string
		}
	}
	if err = json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Rows) != len(rows) {
		t.Fatal("Source rows missing", len(source.Rows))
	}
	for i, chunks := range rows {
		original := source.Rows[i]
		if original.Disk != nil {
			if err = os.Remove(original.Path); err != nil {
				t.Fatal(err)
			}
		}
		output := newAsyncTaskOutput(nil)
		output.setPath(original.Path)
		for _, chunk := range chunks {
			if chunk["spill"] == true {
				if err = output.spill(); err != nil {
					t.Fatal(err)
				}
				continue
			}
			pipe := asyncOutputPipe{output: output, stderr: chunk["stderr"] == true}
			if _, err = pipe.Write([]byte(chunk["text"].(string))); err != nil {
				t.Fatal(err)
			}
		}
		output.finish()
		got, err := output.snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if got.Stdout != original.Stdout || got.Stderr != original.Stderr {
			t.Errorf("case%d output differs; Go stdout %q stderr %q Source stdout %q stderr %q", i, got.Stdout, got.Stderr, original.Stdout, original.Stderr)
		}
		if original.Disk != nil {
			disk, err := os.ReadFile(original.Path)
			if err != nil || string(disk) != *original.Disk {
				t.Errorf("case%d actual spilled bytes differ; lengths Go%d Source%d error%v", i, len(disk), len(*original.Disk), err)
			}
		}
		output.clear()
	}
}
