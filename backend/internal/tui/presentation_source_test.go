package tui

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

func TestReadToolTextPresentationAgainstActualSourceRenderer(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	_ = os.MkdirAll(filepath.Join(home, ".git"), 0700)
	reads := []json.RawMessage{[]byte(`{"type":"text","file":{"filePath":"/fixture/a","content":"actual","numLines":1,"startLine":1,"totalLines":1}}`), []byte(`{"type":"text","file":{"filePath":"/fixture/a","content":"actual","numLines":17,"startLine":2,"totalLines":18}}`), []byte(`{"type":"file_unchanged","file":{"filePath":"/fixture/a"}}`)}

	extra := []string{
		`{"type":"image","file":{"base64":"a","type":"image/png","originalSize":1280}}`,
		`{"type":"parts","file":{"filePath":"/a","outputDir":"/parts","count":1e21,"originalSize":1e34}}`,
		`{"type":"notebook","file":{"filePath":"/a","cells":[]}}`,
		`{"type":"notebook","file":{"filePath":"/a","cells":[{},null,"old"]}}`,
		`{"type":"image","file":{"base64":"actual","type":"image/png","originalSize":1024,"dimensions":{"originalWidth":10}}}`,
		`{"type":"image","file":{"base64":"actual","type":"image/webp","originalSize":128}}`,
		`{"type":"pdf","file":{"filePath":"/a","base64":"actual","originalSize":1536}}`,
		`{"type":"parts","file":{"filePath":"/a","outputDir":"/parts","count":1,"originalSize":1048576}}`,
		`{"type":"parts","file":{"filePath":"/a","outputDir":"/parts","count":7,"originalSize":1073741824}}`,
		`null`, `{}`, `{"type":"text","file":{"numLines":7}}`,
		`{"type":"notebook","file":{"filePath":"/a","cells":null}}`,
		`{"type":"image","file":{"base64":"a","type":"image/png","originalSize":3,"dimensions":null}}`,
		`{"type":"image","file":{"base64":"a","type":"other","originalSize":3}}`,
		`{"type":"file_unchanged","file":{"filePath":null}}`,
		`{"type":"parts","file":{"filePath":"/a","outputDir":"/parts","count":"3","originalSize":8}}`,
		`{"type":"image","file":{"base64":"a","type":"image/png","originalSize":3,"dimensions":{"displayWidth":null}}}`,
		`{"type":"text","future":true,"file":{"filePath":"/a","content":"actual","numLines":-1,"startLine":0,"totalLines":1,"future":{"keep":true}}}`,
	}
	for _, value := range extra {
		reads = append(reads, json.RawMessage(value))
	}
	fixture, _ := filepath.Abs("testdata/presentation.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TUI_PRESENTATION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "reads": reads, "reminders": []string{}})
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source renderer %v %.3500s", e, out)
	}
	var source struct {
		Rows         []*string
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(reads) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.3500s", e, out)
	}
	for i, value := range reads {
		got := toolResultPresentation(Entry{ToolName: "Read", Result: value}, false)
		if source.Rows[i] == nil {
			if got != "" {
				t.Errorf("invalid row%d Go%q Source hidden", i, got)
			}
			continue
		}
		if got != *source.Rows[i] {
			t.Errorf("row%d Go%q Source%v", i, got, source.Rows[i])
		}
	}
}
