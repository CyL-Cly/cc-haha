package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSourceTerminalPasteOrderingDefaultsAndOriginalCacheBytes(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 12, 8))); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(pngData.Bytes())
	rows := []tui.Input{
		{Text: "Inspect [Image #7] and [Image #2]", Mode: "prompt", PastedContents: map[int]tui.PastedContent{7: {ID: 7, Type: "image", Content: encoded, MediaType: "image/png"}, 2: {ID: 2, Type: "image", Content: encoded}}},
		{Mode: "prompt", PastedContents: map[int]tui.PastedContent{3: {ID: 3, Type: "image", Content: encoded}}},
		{Text: "Already expanded literal [Pasted text #1]", Mode: "prompt", PastedContents: map[int]tui.PastedContent{1: {ID: 1, Type: "text", Content: "literal [Pasted text #1]"}}},
		{Text: "/image-check inspect current paste", Mode: "prompt", PastedContents: map[int]tui.PastedContent{7: {ID: 7, Type: "image", Content: encoded, MediaType: "image/png"}}},
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-pasted-media.fixture.ts")
	payloadRows := make([]any, len(rows))
	for i, row := range rows {
		payloadRows[i] = map[string]any{"text": row.Text, "pastes": row.PastedContents, "slash": i == 3}
	}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "rows": payloadRows})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_PASTED_MEDIA_FIXTURE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		SessionID string
		Rows      []struct {
			Content  json.RawMessage
			IDs      []int
			Metadata []string
		}
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatal(err, string(output))
	}
	if len(source.Rows[3].IDs) != 0 || len(source.Rows[3].Metadata) != 1 {
		t.Fatal("actual Source slash must retain metadata without text-prompt display IDs", source.Rows[3])
	}
	for i, row := range rows {
		_, content, err := terminalInputMedia(context.Background(), row, home, filepath.Join(home, ".claude"), source.SessionID, nil)
		if err != nil {
			t.Fatal(err)
		}
		if content == nil {
			content, _ = json.Marshal(row.Text)
		}
		var actual, expected any
		if json.Unmarshal(content, &actual) != nil || json.Unmarshal(source.Rows[i].Content, &expected) != nil || !reflect.DeepEqual(actual, expected) {
			t.Errorf("Source row %d content differs Go=%s Source=%s", i, content, source.Rows[i].Content)
		}
	}
	for _, id := range []string{"2", "3", "7"} {
		path := filepath.Join(home, ".claude", "image-cache", source.SessionID, id+".png")
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, pngData.Bytes()) {
			t.Fatal("Source/Go original cache bytes differ", path, err)
		}
	}
}
