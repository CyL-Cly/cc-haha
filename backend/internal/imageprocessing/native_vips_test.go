//go:build darwin || linux

package imageprocessing

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBundledVipsPhysicalMetadataAndEncode(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("fixture native library is macOS arm64")
	}
	library, _ := filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	processor, err := OpenVips(library)
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
	metadata, err := processor.Metadata(encoded.Bytes())
	if err != nil || metadata != (Metadata{12, 8, "png"}) {
		t.Fatal(metadata, err)
	}
	for _, format := range []string{"png", "jpeg", "webp"} {
		data, err := processor.Encode(encoded.Bytes(), 3, 2, format, 80)
		if err != nil {
			t.Fatal(format, err)
		}
		metadata, err := processor.Metadata(data)
		if err != nil || metadata.Width != 3 || metadata.Height != 2 || metadata.Format != format {
			t.Fatal(format, metadata, err)
		}
	}
}
