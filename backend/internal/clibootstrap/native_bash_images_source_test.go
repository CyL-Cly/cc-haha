package clibootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Real Bash emits the bytes into the production foreground/task/persistence
// consumer. The oracle calls the actual Source resizeShellImageOutput and
// Bash mapper on those same private bytes. It does not substitute a Source
// ShellCommand or claim that the Source Bash process was executed here.
func TestPhysicalBashImagesUseActualSourceNormalizationAndPersistedWholeFile(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) { n.Environment["BASH_MAX_OUTPUT_LENGTH"] = "150000" }, func(options *MainPrintOptions) {
		options.Native.ImageLibraryPath, _ = filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	})
	const tiny = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jBuoAAAAASUVORK5CYII="
	bitmap := image.NewNRGBA(image.Rect(0, 0, 400, 400))
	var random uint32 = 117
	for y := 0; y < 400; y++ {
		for x := 0; x < 400; x++ {
			random = random*1664525 + 1013904223
			bitmap.SetNRGBA(x, y, color.NRGBA{byte(random), byte(random >> 8), byte(random >> 16), 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, bitmap); err != nil {
		t.Fatal(err)
	}
	large := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
	if len(large) < 150000 {
		t.Fatal("physical image fixture does not exceed actual output preview", len(large))
	}
	rows := []map[string]any{}
	produced, mapped := []protocol.JSONObject{}, []protocol.JSONObject{}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for index, content := range []string{tiny, large, tiny + "\nEXTRA_TEXT", "data:image/png;base64," + strings.Repeat("A", 21*1024*1024)} {
		seed := filepath.Join(m.options.Native.CWD, "shell-image-"+string(rune('0'+index))+".txt")
		if err := os.WriteFile(seed, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		var data, result protocol.JSONObject
		err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			output, err := m.nativeTools.CallTerminalBash(ctx, scope, "cat '"+seed+"'", nil)
			if err != nil {
				return err
			}
			if err = data.UnmarshalJSON(output); err != nil {
				return err
			}
			result, err = m.nativeTools.MapTerminalBashResult(scope, output, quoteMain("physical-bash-image-"+string(rune('0'+index))))
			return err
		})
		if err != nil {
			t.Fatal(index, err)
		}
		if index == 1 || index == 3 {
			persisted, err := os.ReadFile(data.String("persistedOutputPath"))
			if err != nil || !bytes.Equal(persisted, []byte(content)) {
				t.Fatal("foreground consumer lost original full image file", index, err)
			}
			if len(data.String("stdout")) > 150000 && index == 3 {
				t.Fatal("oversized image bypassed bounded preview")
			}
		}
		preview := content
		path := ""
		if index == 1 || index == 3 {
			preview, path = content[:150000], seed
		}
		rows = append(rows, map[string]any{"stdout": preview, "path": path, "size": len(content), "data": data, "id": "physical-bash-image-" + string(rune('0'+index))})
		produced, mapped = append(produced, data), append(mapped, result)
	}
	n := m.options.Native
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-windows-shell.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	child := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, bun, "--no-env-file", fixture)
	child.Dir = n.Home
	child.Env = []string{"HOME=" + n.Home, "USERPROFILE=" + n.Home, "CLAUDE_CONFIG_DIR=" + n.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + n.Temp, "CLAUDE_CODE_TMPDIR=" + n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(n.Home), "CC_HAHA_NATIVE_WINDOWS_SHELL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=offline-bash-image-source", "BASH_MAX_OUTPUT_LENGTH=150000", "PATH=/usr/bin:/bin"}
	child.Stdin = bytes.NewReader(rawMain(map[string]any{"home": n.Home, "cwd": n.CWD, "sessionId": m.Controller.SessionID(), "shellImages": rows}))
	var stderr bytes.Buffer
	child.Stderr = &stderr
	output, err := child.Output()
	if err != nil {
		t.Fatal("actual Source image normalization/mapper", err, stderr.String())
	}
	var source struct{ ShellImages []protocol.JSONObject }
	if err = json.Unmarshal(output, &source); err != nil || len(source.ShellImages) != len(rows) {
		t.Fatal(err, string(output))
	}
	for index, expected := range source.ShellImages {
		if !bytes.Equal(produced[index].Get("isImage"), expected.Get("isImage")) || produced[index].String("stdout") != expected.String("stdout") {
			t.Fatal("Source image normalization differs", index, string(produced[index].Get("isImage")), string(expected.Get("isImage")), len(produced[index].String("stdout")), len(expected.String("stdout")))
		}
		var left, right any
		_ = json.Unmarshal(rawMain(mapped[index]), &left)
		_ = json.Unmarshal(expected.Get("mapped"), &right)
		if !reflect.DeepEqual(left, right) {
			t.Fatal("Source image result mapper differs", index)
		}
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}
