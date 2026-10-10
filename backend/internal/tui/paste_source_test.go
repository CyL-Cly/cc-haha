package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestActualSourcePasteReferenceFunctions(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source comparison requires explicitly supplied Bun 1.3.14")
	}
	source, err := filepath.Abs("testdata/paste.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	for _, leaf := range []string{"", "tmp", "config", "cache"} {
		if err := os.MkdirAll(filepath.Join(home, leaf), 0700); err != nil {
			t.Fatal(err)
		}
	}
	text := "中文 😀 [Pasted text #1 +9 lines] [Image #2] [Pasted text #3...] [Image #4] [Pasted text #0]"
	contents := map[int]PastedContent{1: {ID: 1, Type: "text", Content: "literal [Pasted text #3] 👋"}, 2: {ID: 2, Type: "image", Content: "original-image"}, 3: {ID: 3, Type: "text", Content: "line1\nline2"}, 4: {ID: 4, Type: "text", Content: "text entry with Image reference"}}
	formats := []string{"中文 👩‍💻", "one\ntwo\nthree", "trailing\n"}
	raw, _ := json.Marshal(map[string]any{"home": home, "text": text, "contents": contents, "formats": formats})
	cmd := exec.Command(bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", source)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(raw)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "USER_TYPE=external", "ANTHROPIC_API_KEY=fixture", "CC_HAHA_PASTE_FIXTURE=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil || stderr.Len() != 0 {
		t.Fatalf("Source paste failed %v: %s", err, stderr.String())
	}
	var expected struct {
		Expanded     string
		Formats      []string
		Capabilities struct{ Blocked, SecureReads []any }
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err, string(output))
	}
	if len(expected.Capabilities.Blocked) != 0 || len(expected.Capabilities.SecureReads) != 0 {
		t.Fatal("Source escaped private fixture", string(output))
	}
	if got := ExpandPastedTextRefs(text, contents); got != expected.Expanded {
		t.Fatalf("Go expansion %q Source %q", got, expected.Expanded)
	}
	for index, content := range formats {
		if got := pastedTextRef(index+1, content); got != expected.Formats[index] {
			t.Fatalf("Go pill %q Source %q", got, expected.Formats[index])
		}
	}
}
