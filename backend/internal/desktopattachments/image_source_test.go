//go:build darwin || linux

package desktopattachments

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
)

func TestDesktopImagesActualSourcePhysicalStagingMixedOrderAndFallback(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("fixture native library is macOS arm64")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	library, _ := filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	codec, err := imageprocessing.OpenVips(library)
	if err != nil {
		t.Fatal(err)
	}
	source := image.NewNRGBA(image.Rect(0, 0, 12, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 12; x++ {
			source.SetNRGBA(x, y, color.NRGBA{20, 40, 60, 255})
		}
	}
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	inputPath := filepath.Join(home, "provided.png")
	if err = os.WriteFile(inputPath, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude")
	oldRoot := filepath.Join(home, "previous-config")
	movedImage := filepath.Join(config, "image-cache", "old-native-image.png")
	for _, directory := range []string{filepath.Dir(movedImage), filepath.Join(config, "cc-haha")} {
		if err = os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(movedImage, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	relocation, _ := json.Marshal(map[string]any{"version": 1, "previousRoots": []string{oldRoot}})
	if err = os.WriteFile(filepath.Join(config, "cc-haha", "storage-relocations.json"), relocation, 0600); err != nil {
		t.Fatal(err)
	}
	attachments := []any{
		[]any{map[string]any{"type": "image", "name": "汉字.PNG", "data": "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())}},
		[]any{map[string]any{"type": "file", "path": inputPath}},
		[]any{map[string]any{"type": "image", "name": "original.JPEG", "mimeType": "image/jpeg", "data": base64.StdEncoding.EncodeToString(encoded.Bytes())}, map[string]any{"type": "file", "name": "notes.txt", "data": "YWJj"}, map[string]any{"type": "image", "path": inputPath}},
		[]any{map[string]any{"type": "image", "name": "empty.png", "data": "data:image/png;base64,!"}},
		[]any{map[string]any{"type": "image", "path": filepath.Join(home, "missing.png")}},
		[]any{map[string]any{"type": "image", "name": "odd", "data": "eA=="}},
		[]any{map[string]any{"type": "folder", "path": inputPath, "isDirectory": true}},
		[]any{map[string]any{"type": "image", "path": filepath.Join(oldRoot, "image-cache", "old-native-image.png")}},
	}
	rows := []map[string]any{}
	for i, refs := range attachments {
		for j, content := range []string{"  private image task  ", ""} {
			rows = append(rows, map[string]any{"content": content, "sessionId": fmt.Sprintf("case-%d-%d", i, j), "attachments": refs})
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-desktop-image-attachments.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_DESKTOP_IMAGE_ATTACHMENTS_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "cases": rows})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var expected struct {
		Results []struct {
			Blocks json.RawMessage
			Files  []map[string]string
		}
	}
	if err = json.Unmarshal(raw, &expected); err != nil || len(expected.Results) != len(rows) {
		t.Fatal(err, string(raw))
	}
	if err = os.RemoveAll(filepath.Join(home, ".claude", "uploads")); err != nil {
		t.Fatal(err)
	}
	sequence := 0
	for i, row := range rows {
		refs, _ := json.Marshal(row["attachments"])
		value, err := Build(row["content"].(string), row["sessionId"].(string), refs, Options{HomeDir: home, NewID: func() string { sequence++; return fmt.Sprintf("00000000-0000-4000-8000-%012d", sequence) }, ImageProcessor: func(data []byte, size int, ext string) (imageprocessing.Result, error) {
			return imageprocessing.Normalize(codec, data, size, ext)
		}})
		if err != nil || !equalJSON(value, expected.Results[i].Blocks) {
			t.Errorf("case%d Go%s Source%s error%v", i, value, expected.Results[i].Blocks, err)
		}
		dir := filepath.Join(home, ".claude", "uploads", row["sessionId"].(string))
		entries, _ := os.ReadDir(dir)
		files := []map[string]string{}
		for _, entry := range entries {
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, map[string]string{"name": entry.Name(), "data": base64.StdEncoding.EncodeToString(data)})
		}
		a, _ := json.Marshal(files)
		b, _ := json.Marshal(expected.Results[i].Files)
		if !equalJSON(a, b) {
			t.Errorf("case%d physical staging Go%s Source%s", i, a, b)
		}
	}
	retained, err := os.ReadFile(movedImage)
	if err != nil || !bytes.Equal(retained, encoded.Bytes()) {
		t.Fatal("relocated original image bytes changed", err)
	}
}
