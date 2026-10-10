package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/imagegeneration"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func TestNativeImageReadSourceActualCachePermissionsAtMentionAuthorizationAndProjection(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("fixture native library is macOS arm64")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	m := openAIAuthMainFixture(t)
	library, _ := filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	m.options.Native.ImageLibraryPath = library
	cases := []map[string]any{}
	for i, size := range [][2]int{{12, 8}, {9001, 3}} {
		source := image.NewNRGBA(image.Rect(0, 0, size[0], size[1]))
		for y := 0; y < size[1]; y++ {
			for x := 0; x < size[0]; x++ {
				source.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), uint8(x + y), 137})
			}
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, source); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(m.options.Native.Home, fmt.Sprintf("provided-%d.png", i))
		if err := os.WriteFile(path, encoded.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, map[string]any{"path": path, "tokens": 25000})
	}
	empty := filepath.Join(m.options.Native.Home, "empty.png")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cases = append(cases, map[string]any{"path": empty, "tokens": 25000})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-image-read.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = m.options.Native.Home
	cmd.Env = []string{"HOME=" + m.options.Native.Home, "USERPROFILE=" + m.options.Native.Home, "CLAUDE_CONFIG_DIR=" + m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + m.options.Native.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(m.options.Native.Home), "CC_HAHA_IMAGE_READ_FIXTURE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(cases)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var expected []json.RawMessage
	if err = json.Unmarshal(raw, &expected); err != nil || len(expected) != len(cases) {
		t.Fatal(err, string(raw))
	}
	err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, tools, err := m.nativeTools.Catalog(scope)
		if err != nil {
			return err
		}
		var read queryloop.RegisteredTool
		for _, tool := range tools {
			if tool.Name == "Read" {
				read = tool
			}
		}
		for i, row := range cases {
			input := protocol.NewJSONObject(protocol.JSONField{Name: "file_path", Value: quoteMain(row["path"].(string))})
			data, err := m.nativeTools.Call(context.Background(), "Read", input, scope, false)
			actual := map[string]any{"value": data}
			if err != nil {
				actual = map[string]any{"error": err.Error()}
			}
			encoded, _ := json.Marshal(actual)
			var got, want any
			_ = json.Unmarshal(encoded, &got)
			_ = json.Unmarshal(expected[i], &want)
			if !reflect.DeepEqual(got, want) {
				return fmt.Errorf("actual image Read case%d Go%v Source%v", i, got, want)
			}
			if err == nil {
				mapped, err := builtintools.MapResult("Read", data, quoteMain("physical-read"))
				if err != nil {
					return err
				}
				var blocks []protocol.JSONObject
				if json.Unmarshal(mapped.Get("content"), &blocks) != nil || len(blocks) != 1 || blocks[0].String("type") != "image" {
					return fmt.Errorf("image mapped as text: %s", rawMain(mapped))
				}
				future, err := read.StartCallJSON(context.Background(), input, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
				if err != nil {
					return err
				}
				output, err := future.Await(scope)
				if err != nil {
					return err
				}
				expectedMetadata := 0
				if i == 1 {
					expectedMetadata = 1
				}
				if len(output.NewMessages) != expectedMetadata {
					return fmt.Errorf("native Read metadata messages case%d=%d", i, len(output.NewMessages))
				}
			}
		}
		path := cases[0]["path"].(string)
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		if m.userImages.ContainsResolved(resolved) {
			return fmt.Errorf("plain Read authorized image upload")
		}
		payload, err := m.nativeTools.SkillFileAttachment(context.Background(), scope, skillcommands.MentionedFile{Filename: path})
		if err != nil {
			return err
		}
		var attachment protocol.JSONObject
		if err = attachment.UnmarshalJSON(payload); err != nil {
			return err
		}
		content, _ := attachment.Object("content")
		if attachment.String("type") != "file" || content.String("type") != "image" || !m.userImages.ContainsResolved(resolved) {
			return fmt.Errorf("actual @ image producer not registered: %s", payload)
		}
		projected, err := attachments.ProjectWithOptions(payload, m.factory, &attachments.PlanOptions{MapToolResult: builtintools.MapResult})
		if err != nil {
			return err
		}
		if len(projected) != 2 {
			return fmt.Errorf("actual @ image projections %d", len(projected))
		}
		message, _ := projected[1].Object("message")
		var blocks []protocol.JSONObject
		if json.Unmarshal(message.Get("content"), &blocks) != nil || len(blocks) != 1 || blocks[0].String("type") != "image" {
			return fmt.Errorf("@ image transformed to text: %s", rawMain(projected[1]))
		}
		// Same resolved-path registry used by ImageEdit; no model loop or second
		// attachment store is involved in this admission decision.
		files := nativeOwnedImageFiles{m, scope, imagegeneration.NativeFiles{Check: m.options.Native.check}}
		prepared, err := imagegeneration.PrepareInputImages(context.Background(), files, []string{path}, nil, m.userImages.ContainsResolved)
		if err != nil || len(prepared) != 1 {
			return fmt.Errorf("real @ input not accepted by editor: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
