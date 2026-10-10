package mcpruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestActualMCPImageAndEmbeddedResourceUseBundledNormalizationBytesAndDimensionCap(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	cases := []json.RawMessage{}
	for _, width := range []int{12, 8010} {
		pixels := image.NewNRGBA(image.Rect(0, 0, width, 9))
		for y := 0; y < 9; y++ {
			for x := 0; x < width; x++ {
				pixels.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 7), G: uint8(y * 31), B: uint8(x + y), A: 137})
			}
		}
		var buffer bytes.Buffer
		if e = png.Encode(&buffer, pixels); e != nil {
			t.Fatal(e)
		}
		data := base64.StdEncoding.EncodeToString(buffer.Bytes())
		cases = append(cases, resultRaw(map[string]any{"content": []any{map[string]any{"type": "image", "data": data, "mimeType": "image/png"}, map[string]any{"type": "resource", "resource": map[string]any{"uri": "owned:///image", "mimeType": "image/png", "blob": data}}}, "structuredContent": map[string]bool{"image": true}}))
	}
	want := runMCPRichSource(t, home, cases, false)
	library, e := filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(library); e != nil {
		t.Fatal("actual packaged native image library missing", e)
	}
	var wantRows []json.RawMessage
	json.Unmarshal(want.Get("rows"), &wantRows)
	for i, p := range cases {
		settled := make(chan struct{})
		got, e := TransformRichResult(context.Background(), resultObject(p), "contract-tool", "claude.ai 🧡 @blob", RichResultOptions{Directory: want.String("directory"), ImageLibraryPath: library, OnSettled: func() { close(settled) }})
		<-settled
		if e != nil {
			t.Fatal(e)
		}
		row := resultObject(wantRows[i])
		if !jsonEqual(got, row.Get("content")) {
			t.Fatalf("actual Source bundled image normalization case%d differs", i)
		}
	}
}
