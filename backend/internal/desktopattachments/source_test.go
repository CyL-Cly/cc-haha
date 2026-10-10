package desktopattachments

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestActualOriginalDesktopFileAttachmentProducerAndPhysicalBytes(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	for _, dir := range []string{"tmp", ".claude"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	attachmentRows := []string{`null`, `[]`, `[{"type":"file","path":"/private/quoted path.txt"}]`, `[{"type":"folder","path":"/private/image.png","isDirectory":true}]`, `[{"type":"file","name":"中文👋.TXT","data":"data:text/plain;base64,SGVsbG8=","future":"keep"}]`, `[{"type":"file","data":"SGV!sbG8===ignored","mimeType":"application/x-custom+json"}]`, `[{"type":"file","name":"empty.txt","data":"!"}]`, `[{"type":"file","name":"first.bin","data":"_w"},{"type":"file","path":"literal\" path"},{"type":"file","name":"last.md","data":"YQ"}]`, `[{"type":"file","name":"skip.txt","data":""}]`}
	type inputCase struct {
		Content, SessionID string          `json:"-"`
		Attachments        json.RawMessage `json:"attachments"`
	}
	rows := []map[string]any{}
	for i, attachments := range attachmentRows {
		for j, content := range []string{"  analyze\ufeff ", " \t\u00a0 ", "literal @old \u0085"} {
			rows = append(rows, map[string]any{"content": content, "sessionId": fmt.Sprintf("case-%d-%d", i, j), "attachments": json.RawMessage(attachments)})
		}
	}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "cases": rows})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-desktop-attachments.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_DESKTOP_ATTACHMENTS_FIXTURE=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal("actual original attachment producer", err, stderr.String())
	}
	type receipt struct {
		Blocks json.RawMessage     `json:"blocks"`
		Files  []map[string]string `json:"files"`
	}
	var expected struct {
		Results []receipt `json:"results"`
	}
	if err = json.Unmarshal(output, &expected); err != nil || len(expected.Results) != len(rows) {
		t.Fatal("original receipt", err, string(output))
	}
	if err = os.RemoveAll(filepath.Join(home, ".claude", "uploads")); err != nil {
		t.Fatal(err)
	}
	sequence := 0
	for i, row := range rows {
		id := row["sessionId"].(string)
		got, err := Build(row["content"].(string), id, row["attachments"].(json.RawMessage), Options{HomeDir: home, ConfigDir: filepath.Join(home, ".claude"), NewID: func() string { sequence++; return fmt.Sprintf("00000000-0000-4000-8000-%012d", sequence) }})
		if err != nil || !equalJSON(got, expected.Results[i].Blocks) {
			t.Fatalf("case %d Go=%s original=%s error=%v", i, got, expected.Results[i].Blocks, err)
		}
		files := []map[string]string{}
		entries, readErr := os.ReadDir(filepath.Join(home, ".claude", "uploads", id))
		if readErr != nil && !os.IsNotExist(readErr) {
			t.Fatal(readErr)
		}
		for _, entry := range entries {
			data, err := os.ReadFile(filepath.Join(home, ".claude", "uploads", id, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, map[string]string{"name": entry.Name(), "data": base64.StdEncoding.EncodeToString(data)})
		}
		if !reflect.DeepEqual(files, expected.Results[i].Files) {
			t.Fatalf("case%d staged bytes differ: Go%v original%v", i, files, expected.Results[i].Files)
		}
	}
}

func equalJSON(a, b json.RawMessage) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

func TestUnboundImageProducerDoesNotPartiallyStageMixedAttachments(t *testing.T) {
	home := t.TempDir()
	_, err := Build("analyze", "same-session", json.RawMessage(`[{"type":"file","name":"test.txt","data":"YQ=="},{"type":"image","data":"YQ=="}]`), Options{HomeDir: home, NewID: func() string { return "must-not-write" }})
	if !errors.Is(err, ErrImageProducer) {
		t.Fatal("unbound image was silently discarded", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal("rejected mixed input created partial uploads", entries, err)
	}
}
