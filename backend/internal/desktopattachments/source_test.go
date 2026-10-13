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
	config := filepath.Join(home, ".claude")
	oldRoot := filepath.Join(home, "previous-config")
	if err := os.MkdirAll(filepath.Join(config, "cc-haha"), 0700); err != nil {
		t.Fatal(err)
	}
	relocation, _ := json.Marshal(map[string]any{"version": 1, "previousRoots": []string{oldRoot}, "futureMetadata": map[string]any{"keep": true}})
	if err := os.WriteFile(filepath.Join(config, "cc-haha", "storage-relocations.json"), relocation, 0600); err != nil {
		t.Fatal(err)
	}
	movedPath := filepath.Join(config, "uploads", "old-session", "moved.txt")
	stageMovedFile := func() {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(movedPath), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(movedPath, []byte("retained original attachment bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	stageMovedFile()
	attachmentRows := []string{`null`, `[]`, `[{"type":"file","path":"/private/quoted path.txt"}]`, `[{"type":"folder","path":"/private/image.png","isDirectory":true}]`, `[{"type":"file","name":"中文👋.TXT","data":"data:text/plain;base64,SGVsbG8=","future":"keep"}]`, `[{"type":"file","data":"SGV!sbG8===ignored","mimeType":"application/x-custom+json"}]`, `[{"type":"file","name":"empty.txt","data":"!"}]`, `[{"type":"file","name":"first.bin","data":"_w"},{"type":"file","path":"literal\" path"},{"type":"file","name":"last.md","data":"YQ"}]`, `[{"type":"file","name":"skip.txt","data":""}]`}
	for _, path := range []string{filepath.Join(oldRoot, "uploads", "old-session", "moved.txt"), filepath.Join(oldRoot, "project", "keep-external.txt")} {
		encoded, _ := json.Marshal([]map[string]any{{"type": "file", "path": path}})
		attachmentRows = append(attachmentRows, string(encoded))
	}
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
	stageMovedFile()
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
	retained, err := os.ReadFile(movedPath)
	if err != nil || string(retained) != "retained original attachment bytes" {
		t.Fatal("relocated original attachment bytes changed", string(retained), err)
	}
}

func equalJSON(a, b json.RawMessage) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

func TestActualDesktopAttachmentBrokenRelocationMetadataDoesNotFallBackToOldPath(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	for _, fixture := range []struct{ name, metadata string }{
		{"malformed-json", `{"version":`},
		{"unsupported-version", `{"version":2,"previousRoots":[]}`},
		{"malformed-roots", `{"version":1,"previousRoots":[false]}`},
		{"absent-metadata", ""},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(home, ".claude")
			metadataPath := filepath.Join(config, "cc-haha", "storage-relocations.json")
			if err := os.MkdirAll(filepath.Dir(metadataPath), 0700); err != nil {
				t.Fatal(err)
			}
			if fixture.metadata != "" {
				if err := os.WriteFile(metadataPath, []byte(fixture.metadata), 0600); err != nil {
					t.Fatal(err)
				}
			}
			refs, _ := json.Marshal([]map[string]string{{"type": "file", "path": filepath.Join(home, "old-config", "uploads", "original.txt")}})
			payload, _ := json.Marshal(map[string]any{"homeDir": home, "mode": "failure", "cases": []map[string]any{{"content": "read retained file", "sessionId": "metadata-check", "attachments": json.RawMessage(refs)}}})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			path, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-desktop-attachments.fixture.ts")
			child := exec.CommandContext(ctx, bun, "--no-env-file", path)
			child.Dir = home
			child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_DESKTOP_ATTACHMENTS_FIXTURE=1", "PATH=/usr/bin:/bin"}
			child.Stdin = bytes.NewReader(payload)
			var stderr bytes.Buffer
			child.Stderr = &stderr
			output, err := child.Output()
			if err != nil {
				t.Fatal("actual attachment metadata consumer", err, stderr.String())
			}
			var source struct {
				Error  *string
				Blocks json.RawMessage
			}
			if err = json.Unmarshal(output, &source); err != nil {
				t.Fatal(err, string(output))
			}
			blocks, nativeErr := Build("read retained file", "metadata-check", refs, Options{HomeDir: home, ConfigDir: config, NewID: func() string { return "must-not-stage" }})
			if source.Error == nil {
				if nativeErr != nil || !equalJSON(blocks, source.Blocks) {
					t.Fatal("absent relocation metadata altered legacy path contract", nativeErr, string(blocks), string(source.Blocks))
				}
			} else if nativeErr == nil || nativeErr.Error() != *source.Error {
				t.Fatal("broken metadata silently fell back or changed actual error", nativeErr, *source.Error)
			}
			if fixture.metadata != "" {
				retained, err := os.ReadFile(metadataPath)
				if err != nil || string(retained) != fixture.metadata {
					t.Fatal("attachment read repaired protected metadata", string(retained), err)
				}
			}
			if _, err = os.Stat(filepath.Join(config, "uploads", "metadata-check")); !os.IsNotExist(err) {
				t.Fatal("metadata-only path read created staged files", err)
			}
		})
	}
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
